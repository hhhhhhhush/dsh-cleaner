// API 实现：所有会改动磁盘的操作都在这里，统一走 store 层的守护检查。
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dsh-cleaner/internal/config"
	"dsh-cleaner/internal/store"
)

type sessionState struct {
	Home       string             `json:"home"`
	Running    bool               `json:"running"`
	Process    string             `json:"process"`
	Sessions   []store.Session    `json:"sessions"`
	Workspaces []store.Workspace  `json:"workspaces"`
	Archived   int                `json:"archived"`
	Orphan     int                `json:"orphan"`
	TotalBytes int64              `json:"totalBytes"`
	Trash      []store.TrashEntry `json:"trash"`
	TrashBytes int64              `json:"trashBytes"`
}

type failure struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// nonNil 把 nil 切片换成空切片。
// encoding/json 会把 nil 切片写成 null，前端读 xxx.length 会直接抛异常，
// 所以凡是给界面的数组字段都过一遍这里。
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, failure{Error: err.Error()})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	paths := s.current()
	snap, err := store.Scan(paths)
	if err != nil {
		writeErr(w, err)
		return
	}
	running, proc := store.IsRunning()

	archived, orphan := 0, 0
	for _, x := range snap.Sessions {
		if x.Archived {
			archived++
		}
		if !x.InIndex {
			orphan++
		}
	}

	trash := store.ListTrash(paths)
	var trashBytes int64
	for _, t := range trash {
		trashBytes += t.Bytes
	}

	writeJSON(w, http.StatusOK, sessionState{
		Home:       paths.Home,
		Running:    running,
		Process:    proc,
		Sessions:   nonNil(snap.Sessions),
		Workspaces: nonNil(snap.Workspaces),
		Archived:   archived,
		Orphan:     orphan,
		TotalBytes: snap.TotalBytes,
		Trash:      nonNil(trash),
		TrashBytes: trashBytes,
	})
}

func (s *Server) handleCandidates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, nonNil(config.Probe(config.LoadProfile().Home)))
}

func (s *Server) handleSwitchHome(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Home string `json:"home"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Home) == "" {
		writeJSON(w, http.StatusBadRequest, failure{Error: "缺少目录参数"})
		return
	}
	home := filepath.Clean(body.Home)
	// 必须是真实存在的 DSH 数据目录，否则界面会一片空白，像把数据删光了
	idx := filepath.Join(home, config.StoragesDir, config.IndexName)
	if st, err := os.Stat(idx); err != nil || st.IsDir() {
		writeJSON(w, http.StatusBadRequest, failure{Error: "这个目录里没有 DSH 的会话索引，不是数据目录: " + home})
		return
	}
	s.switchHome(home)
	writeJSON(w, http.StatusOK, map[string]string{"home": home})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs  []string `json:"ids"`
		Hard bool     `json:"hard"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, failure{Error: "参数解析失败"})
		return
	}
	res, err := store.RemoveSessions(s.current(), body.IDs, body.Hard)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDeleteArchived(w http.ResponseWriter, r *http.Request) {
	res, err := store.RemoveArchived(s.current(), readHard(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDeleteOrphan(w http.ResponseWriter, r *http.Request) {
	res, err := store.RemoveOrphans(s.current(), readHard(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkspaceID string `json:"workspaceId"`
		Hard        bool   `json:"hard"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.WorkspaceID == "" {
		writeJSON(w, http.StatusBadRequest, failure{Error: "缺少工作区参数"})
		return
	}
	res, err := store.RemoveWorkspace(s.current(), body.WorkspaceID, body.Hard)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string   `json:"dir"`
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Dir == "" {
		writeJSON(w, http.StatusBadRequest, failure{Error: "缺少隔离区参数"})
		return
	}
	n, err := store.Restore(s.current(), body.Dir, body.IDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"restored": n})
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Dir == "" {
		writeJSON(w, http.StatusBadRequest, failure{Error: "缺少隔离区参数"})
		return
	}
	if err := store.Purge(s.current(), body.Dir); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func readHard(r *http.Request) bool {
	var body struct {
		Hard bool `json:"hard"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	return body.Hard
}

// handlePing 界面每几秒调用一次，用于判断窗口是否还开着
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleQuit 界面上的「退出」按钮
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond) // 让响应先写回去
		os.Exit(0)
	}()
}
