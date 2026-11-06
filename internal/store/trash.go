// 隔离区管理：清单、列表、还原、清空。
// 还原会把会话目录移回原位，并重新写回原工作区的 sessionIds。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dsh-cleaner/internal/config"
)

// TrashItem manifest 里的一条记录
type TrashItem struct {
	SessionID   string `json:"sessionId"`
	FromRel     string `json:"fromRel"`
	Title       string `json:"title"`
	WorkspaceID string `json:"workspaceId"`
	Archived    bool   `json:"archived"` // 还原时要把归档状态一起恢复
}

// TrashManifest 一次删除操作的清单
type TrashManifest struct {
	CreatedAt string      `json:"createdAt"`
	Items     []TrashItem `json:"items"`
}

// TrashEntry 界面上的一个隔离区批次
type TrashEntry struct {
	Dir       string      `json:"dir"`
	Name      string      `json:"name"`
	CreatedAt string      `json:"createdAt"`
	Bytes     int64       `json:"bytes"`
	Items     []TrashItem `json:"items"`
}

const manifestName = "manifest.json"

func writeManifest(dir string, items []TrashItem) error {
	m := TrashManifest{
		CreatedAt: time.Now().Format(time.RFC3339),
		Items:     items,
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, manifestName), raw, 0o644)
}

func readManifest(dir string) TrashManifest {
	var m TrashManifest
	raw, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return m
	}
	json.Unmarshal(raw, &m)
	return m
}

// scanTrashDirs 按目录结构重建批次内容。
// 早期版本（bat 脚本时代）写隔离区时不写 manifest，只把
// <批次>/<工作区目录>/<session-xxx>/ 原样搬过来，
// 所以这里扫两层就能还原出「哪些会话、原本属于哪个工作区」。
func scanTrashDirs(dir string) []TrashItem {
	items := []TrashItem{}
	wsEntries, err := os.ReadDir(dir)
	if err != nil {
		return items
	}
	for _, ws := range wsEntries {
		if !ws.IsDir() {
			continue
		}
		subs, err := os.ReadDir(filepath.Join(dir, ws.Name()))
		if err != nil {
			continue
		}
		for _, s := range subs {
			if !s.IsDir() || !strings.HasPrefix(s.Name(), "session-") {
				continue
			}
			items = append(items, TrashItem{
				SessionID: s.Name(),
				FromRel:   filepath.Join(ws.Name(), s.Name()),
			})
		}
	}
	return items
}

// batchItems 返回一个批次的会话清单：优先读 manifest，没有就按目录结构重建
func batchItems(dir string) []TrashItem {
	m := readManifest(dir)
	if len(m.Items) > 0 {
		return m.Items
	}
	return scanTrashDirs(dir)
}

// ListTrash 列出全部隔离区批次，最新的在前
func ListTrash(paths config.Paths) []TrashEntry {
	out := []TrashEntry{}
	entries, err := os.ReadDir(paths.Trash())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), config.TrashPrefix) {
			continue
		}
		dir := filepath.Join(paths.Trash(), e.Name())
		m := readManifest(dir)
		out = append(out, TrashEntry{
			Dir:       dir,
			Name:      e.Name(),
			CreatedAt: m.CreatedAt,
			Bytes:     config.DirSize(dir),
			Items:     batchItems(dir),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

// safeEntry 校验隔离区目录的合法性。
// 还原和清空都会直接操作这个路径，如果只信前端传来的字符串，
// 一个构造出来的请求就能删掉任意目录，所以这里必须守住：
// 必须是 <home>/trash 的直接子目录，且名字带本工具的批次前缀。
func safeEntry(paths config.Paths, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("缺少隔离区参数")
	}
	absDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("路径解析失败: %w", err)
	}
	absTrash, err := filepath.Abs(filepath.Clean(paths.Trash()))
	if err != nil {
		return "", fmt.Errorf("路径解析失败: %w", err)
	}
	if !strings.EqualFold(filepath.Dir(absDir), absTrash) {
		return "", fmt.Errorf("不是隔离区目录，已拒绝: %s", dir)
	}
	if !strings.HasPrefix(filepath.Base(absDir), config.TrashPrefix) {
		return "", fmt.Errorf("不是本工具创建的批次，已拒绝: %s", filepath.Base(absDir))
	}
	return absDir, nil
}

// Restore 把某个批次里的会话移回原位。
// ids 为空表示整批还原；会话所在的工作区若已被删除，则回落到原目录名下，重启后显示为未分组。
func Restore(paths config.Paths, entryDir string, ids []string) (int, error) {
	if running, name := IsRunning(); running {
		return 0, &runningError{name}
	}
	entryDir, err := safeEntry(paths, entryDir)
	if err != nil {
		return 0, err
	}
	m := readManifest(entryDir)
	want := toSet(ids)
	// 没有 manifest 的旧批次按目录结构重建，还原逻辑保持一致
	items := m.Items
	if len(items) == 0 {
		items = scanTrashDirs(entryDir)
	}

	ix, _ := LoadIndex(paths.Index())
	restored := 0
	for _, item := range items {
		if len(want) > 0 && !want[item.SessionID] {
			continue
		}
		// 新批次的会话平铺在批次根下；旧批次保留了工作区层级，两种都要能找到
		src := filepath.Join(entryDir, item.SessionID)
		if _, err := os.Stat(src); err != nil {
			src = filepath.Join(entryDir, item.FromRel)
		}
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dest := filepath.Join(paths.Sessions(), item.FromRel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			continue
		}
		if err := moveDir(src, dest); err != nil {
			continue
		}
		if ix != nil {
			if item.WorkspaceID != "" {
				ix.attachSession(item.WorkspaceID, item.SessionID)
			}
			if item.Archived {
				ix.markArchived(item.SessionID)
			}
		}
		restored++
	}

	if ix != nil && restored > 0 {
		ix.Backup(paths.Backup())
		if err := ix.Save(); err != nil {
			return restored, err
		}
	}
	// 全部还原后清掉空的批次目录
	if !batchHasSessions(entryDir) {
		os.RemoveAll(entryDir)
	}
	return restored, nil
}

// batchHasSessions 判断批次里是否还有会话目录。
// 兼容两种结构：平铺（<批次>/<session-xxx>）和带工作区层级（<批次>/<工作区>/<session-xxx>）。
func batchHasSessions(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "session-") {
			return true
		}
		subs, err := os.ReadDir(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, s := range subs {
			if s.IsDir() && strings.HasPrefix(s.Name(), "session-") {
				return true
			}
		}
	}
	return false
}

// Purge 清空某个隔离区批次（不可恢复）。
// 只接受 <home>/trash 下本工具创建的批次，避免这个接口变成任意目录删除器。
func Purge(paths config.Paths, entryDir string) error {
	dir, err := safeEntry(paths, entryDir)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

type runningError struct{ name string }

func (e *runningError) Error() string {
	return "客户端正在运行，请先完全退出（检测到 " + e.name + "）"
}

// attachSession 还原时把会话重新挂回工作区
func (ix *Index) attachSession(workspaceID, sessionID string) {
	raw, ok := ix.workspacesRaw()[workspaceID].(map[string]any)
	if !ok {
		return
	}
	existing := asStrings(raw["sessionIds"])
	for _, s := range existing {
		if s == sessionID {
			return
		}
	}
	next := make([]any, 0, len(existing)+1)
	for _, s := range existing {
		next = append(next, s)
	}
	next = append(next, sessionID)
	raw["sessionIds"] = next
}
