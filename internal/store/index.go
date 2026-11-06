// 索引（storages/workspace.json）读写。
// 全程使用 map[string]any 完整保留未知字段：客户端升级新增字段时不会因本工具而丢失。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Workspace 工作区视图
type Workspace struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Path       string   `json:"path"`
	SessionIDs []string `json:"sessionIds"`
}

// Index 工作区索引的内存表示
type Index struct {
	path string
	data map[string]any
}

// LoadIndex 读取索引文件
func LoadIndex(path string) (*Index, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("索引文件解析失败: %w", err)
	}
	return &Index{path: path, data: data}, nil
}

// Path 索引文件路径
func (ix *Index) Path() string { return ix.path }

func (ix *Index) global() map[string]any {
	g, ok := ix.data["global"].(map[string]any)
	if !ok {
		g = map[string]any{}
		ix.data["global"] = g
	}
	return g
}

func (ix *Index) workspacesRaw() map[string]any {
	tables, ok := ix.data["tables"].(map[string]any)
	if !ok {
		tables = map[string]any{}
		ix.data["tables"] = tables
	}
	ws, ok := tables["workspaces"].(map[string]any)
	if !ok {
		ws = map[string]any{}
		tables["workspaces"] = ws
	}
	return ws
}

// Workspaces 列出全部工作区，按标题排序
func (ix *Index) Workspaces() []Workspace {
	out := []Workspace{}
	for id, raw := range ix.workspacesRaw() {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, Workspace{
			ID:         id,
			Title:      asString(m["title"]),
			Path:       asString(m["path"]),
			SessionIDs: asStrings(m["sessionIds"]),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// WorkspaceOf 返回某个会话所属的工作区 id，不在任何工作区时返回空
func (ix *Index) WorkspaceOf(sessionID string) string {
	for id, raw := range ix.workspacesRaw() {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, s := range asStrings(m["sessionIds"]) {
			if s == sessionID {
				return id
			}
		}
	}
	return ""
}

// ArchivedSet 已归档的会话 id 集合
func (ix *Index) ArchivedSet() map[string]bool {
	return toSet(asStrings(ix.global()["archivedSessionIds"]))
}

// PinnedSet 已置顶的会话 id 集合
func (ix *Index) PinnedSet() map[string]bool {
	return toSet(asStrings(ix.global()["pinnedSessionIds"]))
}

// DetachSessions 把会话从索引中完全摘除：工作区归属、归档、置顶三处一起清
func (ix *Index) DetachSessions(ids []string) int {
	target := toSet(ids)
	removed := 0

	for _, raw := range ix.workspacesRaw() {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kept := []any{}
		for _, s := range asStrings(m["sessionIds"]) {
			if target[s] {
				continue
			}
			kept = append(kept, s)
		}
		if len(kept) != len(asStrings(m["sessionIds"])) {
			m["sessionIds"] = kept
			removed++
		}
	}

	g := ix.global()
	g["archivedSessionIds"] = filterOut(asStrings(g["archivedSessionIds"]), target)
	g["pinnedSessionIds"] = filterOut(asStrings(g["pinnedSessionIds"]), target)
	return removed
}

// markArchived 把会话重新标记为已归档（还原时用）
func (ix *Index) markArchived(sessionID string) {
	next := []any{}
	for _, s := range asStrings(ix.global()["archivedSessionIds"]) {
		if s == sessionID {
			return
		}
		next = append(next, s)
	}
	ix.global()["archivedSessionIds"] = append(next, sessionID)
}

// DetachWorkspace 删除工作区本身，并把其中的会话从索引里摘掉
func (ix *Index) DetachWorkspace(workspaceID string) {
	ws := ix.workspacesRaw()
	if raw, ok := ws[workspaceID].(map[string]any); ok {
		ix.DetachSessions(asStrings(raw["sessionIds"]))
	}
	delete(ws, workspaceID)

	g := ix.global()
	remaining := []any{}
	for _, id := range asStrings(g["workspaceIds"]) {
		if id != workspaceID {
			remaining = append(remaining, id)
		}
	}
	g["workspaceIds"] = remaining

	// 默认工作区被删时，指向剩下的第一个，避免客户端读到悬空引用
	if asString(g["defaultWorkspaceId"]) == workspaceID {
		if len(remaining) > 0 {
			g["defaultWorkspaceId"] = remaining[0]
		} else {
			delete(g, "defaultWorkspaceId")
		}
	}
}

// Backup 写索引前先备份一份，返回备份路径
func (ix *Index) Backup(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(ix.path)
	if err != nil {
		return "", err
	}
	name := "workspace-" + time.Now().Format("20060102-150405") + ".json"
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// Save 原子写回：先写临时文件再改名，避免断电留下半个文件
func (ix *Index) Save() error {
	raw, err := json.MarshalIndent(ix.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := ix.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ix.path)
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func filterOut(items []string, drop map[string]bool) []any {
	out := []any{}
	for _, s := range items {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}
