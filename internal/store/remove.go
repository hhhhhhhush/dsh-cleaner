// 删除：默认移入隔离区，可随时还原；--hard 才直接从磁盘抹掉。
// 每次都先备份索引，索引改坏了能捞回来。
package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"dsh-cleaner/internal/config"
)

// ErrAppRunning 客户端正在运行，拒绝执行
var ErrAppRunning = errors.New("客户端正在运行，请先完全退出（含托盘图标）")

// Target 一个待删除对象
type Target struct {
	SessionID   string
	FromRel     string // 相对 sessions 目录的路径，用于还原
	Title       string
	WorkspaceID string
	Archived    bool
	Dir         string
}

// RemoveResult 删除结果
type RemoveResult struct {
	Removed  int      `json:"removed"`
	TrashDir string   `json:"trashDir"`
	Backup   string   `json:"backup"`
	Failed   []string `json:"failed"`
	Mode     string   `json:"mode"`
}

// RemoveSessions 按 id 删除会话
func RemoveSessions(paths config.Paths, ids []string, hard bool) (*RemoveResult, error) {
	if running, name := IsRunning(); running {
		return nil, fmt.Errorf("%w（检测到进程 %s）", ErrAppRunning, name)
	}
	if len(ids) == 0 {
		return &RemoveResult{}, nil
	}
	ix, err := LoadIndex(paths.Index())
	if err != nil {
		return nil, err
	}
	archived := ix.ArchivedSet()

	targets := make([]Target, 0, len(ids))
	for _, id := range ids {
		dir, rel := locateSession(paths, id)
		if dir == "" {
			continue
		}
		targets = append(targets, Target{
			SessionID:   id,
			FromRel:     rel,
			Dir:         dir,
			Title:       sessionTitle(dir),
			WorkspaceID: ix.WorkspaceOf(id),
			Archived:    archived[id],
		})
	}

	res, done, err := applyRemoval(paths, ix, targets, hard)
	if err != nil {
		return nil, err
	}
	// 只摘除真正处理成功的会话：文件没删掉却从索引里抹掉，界面上就此消失，最危险
	if len(done) > 0 {
		ix.DetachSessions(done)
		if err := ix.Save(); err != nil {
			return nil, fmt.Errorf("索引写回失败（备份在 %s）: %w", res.Backup, err)
		}
	}
	return res, nil
}

// RemoveWorkspace 删除整个工作区：工作区内会话一并清掉，索引里整体摘除
func RemoveWorkspace(paths config.Paths, workspaceID string, hard bool) (*RemoveResult, error) {
	if running, name := IsRunning(); running {
		return nil, fmt.Errorf("%w（检测到进程 %s）", ErrAppRunning, name)
	}
	ix, err := LoadIndex(paths.Index())
	if err != nil {
		return nil, err
	}
	archived := ix.ArchivedSet()

	var targets []Target
	for _, w := range ix.Workspaces() {
		if w.ID != workspaceID {
			continue
		}
		for _, sid := range w.SessionIDs {
			dir, rel := locateSession(paths, sid)
			if dir == "" {
				continue
			}
			targets = append(targets, Target{
				SessionID:   sid,
				FromRel:     rel,
				Dir:         dir,
				Title:       sessionTitle(dir), // 会话自己的标题，不是工作区名
				WorkspaceID: w.ID,
				Archived:    archived[sid],
			})
		}
	}

	res, done, err := applyRemoval(paths, ix, targets, hard)
	if err != nil {
		return nil, err
	}
	ix.DetachSessions(done)
	ix.DetachWorkspace(workspaceID)
	if err := ix.Save(); err != nil {
		return nil, fmt.Errorf("索引写回失败（备份在 %s）: %w", res.Backup, err)
	}
	return res, nil
}

// sessionTitle 从会话文件里取标题，只用于隔离区清单展示
func sessionTitle(dir string) string {
	file := filepath.Join(dir, config.SessionFile)
	if _, err := os.Stat(file); err != nil {
		return "（空会话 / 无正文）"
	}
	return Summarize(file).Title
}

// locateSession 在 sessions 下找某个会话目录，返回绝对路径与相对路径
func locateSession(paths config.Paths, sessionID string) (string, string) {
	wsEntries, err := os.ReadDir(paths.Sessions())
	if err != nil {
		return "", ""
	}
	for _, ws := range wsEntries {
		if !ws.IsDir() {
			continue
		}
		candidate := filepath.Join(paths.Sessions(), ws.Name(), sessionID)
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, filepath.Join(ws.Name(), sessionID)
		}
	}
	return "", ""
}

// applyRemoval 执行移动或硬删，写 manifest，返回汇总与真正处理成功的 id
func applyRemoval(paths config.Paths, ix *Index, targets []Target, hard bool) (*RemoveResult, []string, error) {
	res := &RemoveResult{Mode: map[bool]string{true: "彻底删除", false: "移入隔离区"}[hard]}

	backup, err := ix.Backup(paths.Backup())
	if err != nil {
		return nil, nil, fmt.Errorf("索引备份失败，已中止: %w", err)
	}
	res.Backup = backup

	var (
		trashDir string
		manifest []TrashItem
		done     []string
	)
	if !hard {
		trashDir, err = newTrashDir(paths.Trash())
		if err != nil {
			return nil, nil, err
		}
	}

	for _, t := range targets {
		if hard {
			if err := os.RemoveAll(t.Dir); err != nil {
				res.Failed = append(res.Failed, t.SessionID+": "+err.Error())
				continue
			}
			res.Removed++
			done = append(done, t.SessionID)
			continue
		}
		dest := filepath.Join(trashDir, t.SessionID)
		if err := moveDir(t.Dir, dest); err != nil {
			res.Failed = append(res.Failed, t.SessionID+": "+err.Error())
			continue
		}
		manifest = append(manifest, TrashItem{
			SessionID:   t.SessionID,
			FromRel:     t.FromRel,
			Title:       t.Title,
			WorkspaceID: t.WorkspaceID,
			Archived:    t.Archived,
		})
		res.Removed++
		done = append(done, t.SessionID)
	}

	if !hard {
		res.TrashDir = trashDir
		if err := writeManifest(trashDir, manifest); err != nil {
			return res, done, fmt.Errorf("隔离区清单写入失败: %w", err)
		}
	}
	return res, done, nil
}

// newTrashDir 新建一个隔离区批次目录。
// 名字精确到秒，同一秒内连续删两次会撞名 —— 撞了就加序号，
// 否则后一次的清单会覆盖前一次，前一批会话就变成「无清单」。
func newTrashDir(root string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	base := filepath.Join(root, config.TrashPrefix+time.Now().Format("20060102-150405"))
	dir := base
	for i := 2; i <= 50; i++ {
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		dir = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("隔离区目录创建失败（同名批次过多）: %s", base)
}

// moveDir 优先改名（同盘瞬时完成），跨盘时回退到复制后删除
func moveDir(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := copyDir(src, dest); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyDir(src, dest string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

// copyFile 逐个文件复制，句柄用完立刻关闭（跨盘移动时可能一次拷几百个文件）
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
