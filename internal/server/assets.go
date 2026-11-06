// 前端资源内嵌进二进制：编译产物是单文件，不需要额外的目录。
package server

import (
	"embed"
	"net/http"
)

//go:embed web
var webFS embed.FS

// handleIndex 返回界面首页，必须带正确 token 才能打开。
// Host 也要校验：否则网页可以用 DNS 重绑定读到界面（index 里带着数据目录和会话标题）。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.URL.Query().Get("t") != s.token {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	s.beat()
	raw, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "界面资源缺失", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

// handleAsset 返回静态资源，内容不含敏感信息，不做 token 校验
func (s *Server) handleAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := webFS.ReadFile("web/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Write(raw)
	}
}
