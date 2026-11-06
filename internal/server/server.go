// Package server 提供只监听 127.0.0.1 的本地服务，供界面调用。
// 安全约定：随机端口 + 一次性 token + 校验 Host，防止本机其他程序或网页调用。
package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dsh-cleaner/internal/config"
)

// Server 持有当前数据目录，切换目录时更新
type Server struct {
	token string
	mu    sync.RWMutex
	paths config.Paths
	// opMu 串行化所有写操作：两个请求同时读索引→改→写回，后写的会覆盖先写的
	opMu sync.Mutex
	// 界面心跳：页面关闭后不再发心跳，主程序据此自动退出
	lastBeat atomic.Int64
}

// New 创建服务实例
func New(paths config.Paths, token string) *Server {
	s := &Server{token: token, paths: paths}
	s.beat()
	return s
}

// Beat 记录一次界面心跳
func (s *Server) beat() { s.lastBeat.Store(time.Now().Unix()) }

// IdleSeconds 返回距上次界面心跳的秒数
func (s *Server) IdleSeconds() int64 { return time.Now().Unix() - s.lastBeat.Load() }

func (s *Server) current() config.Paths {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.paths
}

func (s *Server) switchHome(home string) {
	s.mu.Lock()
	s.paths = config.Paths{Home: home}
	s.mu.Unlock()
	config.Remember(home)
}

// Handler 返回带全部路由的 http.Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /app.js", s.handleAsset("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /style.css", s.handleAsset("style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /favicon.png", s.handleAsset("favicon.png", "image/png"))
	mux.HandleFunc("GET /api/state", s.guard(s.handleState))
	mux.HandleFunc("GET /api/candidates", s.guard(s.handleCandidates))
	mux.HandleFunc("POST /api/switch-home", s.guard(s.serialize(s.handleSwitchHome)))
	mux.HandleFunc("POST /api/delete", s.guard(s.serialize(s.handleDelete)))
	mux.HandleFunc("POST /api/delete-archived", s.guard(s.serialize(s.handleDeleteArchived)))
	mux.HandleFunc("POST /api/delete-orphan", s.guard(s.serialize(s.handleDeleteOrphan)))
	mux.HandleFunc("POST /api/delete-workspace", s.guard(s.serialize(s.handleDeleteWorkspace)))
	mux.HandleFunc("POST /api/restore", s.guard(s.serialize(s.handleRestore)))
	mux.HandleFunc("POST /api/purge", s.guard(s.serialize(s.handlePurge)))
	mux.HandleFunc("POST /api/ping", s.guard(s.handlePing))
	mux.HandleFunc("POST /api/quit", s.guard(s.handleQuit))
	return mux
}

// Listen 在随机端口启动，返回实际监听地址
func (s *Server) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return ln, nil
}

// serialize 让写操作串行执行。
// 索引是「读整份 → 改 → 写回」，两个请求并发时会互相覆盖，必须排队。
func (s *Server) serialize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.opMu.Lock()
		defer s.opMu.Unlock()
		next(w, r)
	}
}

// hostAllowed 只接受本机 Host，挡住 DNS 重绑定
func (s *Server) hostAllowed(r *http.Request) bool {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host == "127.0.0.1" || host == "localhost"
}

// guard 校验 token 与 Host，挡住同机其他程序与 DNS 重绑定
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Token") != s.token {
			http.Error(w, "invalid token", http.StatusForbidden)
			return
		}
		s.beat()
		next(w, r)
	}
}
