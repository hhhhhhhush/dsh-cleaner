// 扫描：把磁盘上的会话读成结构化清单（只读，不修改任何文件）
package store

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"dsh-cleaner/internal/config"
)

// Session 送给界面的一条会话
type Session struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspaceId"`
	WorkspaceTitle string `json:"workspaceTitle"`
	WorkspacePath  string `json:"workspacePath"`
	Title          string `json:"title"`
	CreatedAt      int64  `json:"createdAt"`
	LastTime       int64  `json:"lastTime"`
	Bytes          int64  `json:"bytes"`
	Archived       bool   `json:"archived"`
	Pinned         bool   `json:"pinned"`
	InIndex        bool   `json:"inIndex"`
	Dir            string `json:"dir"`
}

// Snapshot 一次扫描的完整结果
type Snapshot struct {
	Home       string      `json:"home"`
	Sessions   []Session   `json:"sessions"`
	Workspaces []Workspace `json:"workspaces"`
	TotalBytes int64       `json:"totalBytes"`
}

// Scan 扫描全部会话，并发解析会话正文以缩短等待
func Scan(paths config.Paths) (*Snapshot, error) {
	ix, err := LoadIndex(paths.Index())
	if err != nil {
		return nil, err
	}
	archived := ix.ArchivedSet()
	pinned := ix.PinnedSet()
	workspaces := ix.Workspaces()

	wsTitle := map[string]string{}
	wsPath := map[string]string{}
	for _, w := range workspaces {
		wsTitle[w.ID] = w.Title
		wsPath[w.ID] = w.Path
	}

	type job struct {
		id    string
		dir   string
		wsID  string
		wsDir string
	}
	var jobs []job
	wsEntries, err := os.ReadDir(paths.Sessions())
	if err != nil {
		return nil, err
	}
	for _, wsDir := range wsEntries {
		if !wsDir.IsDir() {
			continue
		}
		sub, err := os.ReadDir(filepath.Join(paths.Sessions(), wsDir.Name()))
		if err != nil {
			continue
		}
		for _, sDir := range sub {
			if !sDir.IsDir() {
				continue
			}
			id := sDir.Name()
			jobs = append(jobs, job{
				id:    id,
				dir:   filepath.Join(paths.Sessions(), wsDir.Name(), id),
				wsID:  ix.WorkspaceOf(id),
				wsDir: wsDir.Name(),
			})
		}
	}

	sessions := make([]Session, len(jobs))
	// 并发解析会话正文：会话多的时候（几百个）串行解压会让界面卡住
	workers := runtime.NumCPU()
	if workers > 16 {
		workers = 16
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			s := Session{
				ID:          j.id,
				Dir:         j.dir,
				WorkspaceID: j.wsID,
				Bytes:       config.DirSize(j.dir),
				Archived:    archived[j.id],
				Pinned:      pinned[j.id],
				InIndex:     j.wsID != "",
			}
			if title, ok := wsTitle[j.wsID]; ok {
				s.WorkspaceTitle = title
				s.WorkspacePath = wsPath[j.wsID]
			}

			file := filepath.Join(j.dir, config.SessionFile)
			if _, statErr := os.Stat(file); statErr != nil {
				s.Title = "（空会话 / 无正文）"
			} else {
				sum := Summarize(file)
				s.Title = sum.Title
				s.CreatedAt = sum.CreatedAt
				s.LastTime = sum.LastTime
				if sum.Cwd != "" {
					s.WorkspacePath = sum.Cwd
				}
			}
			// 没有标题也没有工作区归属时，用目录名还原出路径，避免界面上一片空白
			if s.WorkspacePath == "" {
				s.WorkspacePath = decodeWorkspaceDir(j.wsDir)
			}
			sessions[i] = s
		}(i, j)
	}
	wg.Wait()

	var total int64
	for _, s := range sessions {
		total += s.Bytes
	}

	sort.SliceStable(sessions, func(a, b int) bool {
		if sessions[a].WorkspacePath != sessions[b].WorkspacePath {
			return sessions[a].WorkspacePath < sessions[b].WorkspacePath
		}
		if sessions[a].LastTime != sessions[b].LastTime {
			return sessions[a].LastTime > sessions[b].LastTime
		}
		return sessions[a].Title < sessions[b].Title
	})

	return &Snapshot{
		Home:       paths.Home,
		Sessions:   sessions,
		Workspaces: workspaces,
		TotalBytes: total,
	}, nil
}

// decodeWorkspaceDir 把 sessions 下的工作区目录名还原成路径。
// 规则：`--E-ds~539F~751F--` → `E:\ds原生`，即 `-` 是路径分隔符、`~XXXX` 是 Unicode 码点。
func decodeWorkspaceDir(name string) string {
	trimmed := strings.Trim(name, "-")
	if trimmed == "" {
		return name
	}
	parts := strings.Split(trimmed, "-")
	decoded := make([]string, 0, len(parts))
	for _, p := range parts {
		decoded = append(decoded, decodeTilde(p))
	}
	out := strings.Join(decoded, `\`)
	// 盘符后面补冒号：`E\ds` → `E:\ds`
	if len(out) > 1 && out[1] == '\\' {
		out = out[:1] + ":" + out[1:]
	}
	return out
}

// decodeTilde 把 `~XXXX` 形式的十六进制码点还原成字符
func decodeTilde(s string) string {
	if !strings.Contains(s, "~") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '~' && i+5 <= len(s) {
			if v, err := strconv.ParseInt(s[i+1:i+5], 16, 32); err == nil {
				b.WriteRune(rune(v))
				i += 5
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
