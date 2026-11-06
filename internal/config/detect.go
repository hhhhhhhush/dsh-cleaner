// 数据目录探测：不做猜测式自动选择，而是列出所有有效候选交由用户确认。
// 猜错目录会导致删错数据，这是本工具唯一不可接受的错误。
package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Candidate 一个可能的数据根目录
type Candidate struct {
	Home         string `json:"home"`
	Source       string `json:"source"`       // 来源说明，用于界面展示
	Valid        bool   `json:"valid"`        // 有索引或会话目录
	SessionCount int    `json:"sessionCount"` // 会话文件数，用于排序与判断
	HasIndex     bool   `json:"hasIndex"`
	SizeBytes    int64  `json:"sizeBytes"`
}

// RawCandidate 未探测的候选（路径 + 来源）
type RawCandidate struct {
	Home   string
	Source string
}

// RawCandidates 按优先级返回所有可能的根目录，已去重
func RawCandidates(remembered string) []RawCandidate {
	var out []RawCandidate
	seen := map[string]bool{}
	add := func(home, source string) {
		if home == "" {
			return
		}
		clean := filepath.Clean(home)
		key := strings.ToLower(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, RawCandidate{Home: clean, Source: source})
	}

	if remembered != "" {
		add(remembered, "上次使用的目录")
	}
	if env := strings.TrimSpace(os.Getenv(HomeEnv)); env != "" {
		add(expandTilde(env), "$DSH_HOME 环境变量")
	}

	userHome := UserHome()
	add(filepath.Join(userHome, ".qurwork-desktop-dsh", ".dsh"), "桌面客户端默认位置")
	add(filepath.Join(userHome, ".dsh"), "官方默认位置 ~/.dsh")

	for _, root := range Drives() {
		add(filepath.Join(root, "DSH-Home"), "磁盘根目录扫描")
		add(filepath.Join(root, "dsh-home"), "磁盘根目录扫描")
		add(filepath.Join(root, ".dsh"), "磁盘根目录扫描")
	}
	return out
}

// expandTilde 支持 ~ / ~/ / ~\ 前缀，与官方 resolveDshHome 行为一致
func expandTilde(p string) string {
	if p == "~" {
		return UserHome()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(UserHome(), p[2:])
	}
	return p
}

// LooksLikeHome 判断一个路径是否还是有效的数据目录（索引或会话目录存在）。
// 用于启动时校验"上次记住的目录"——它可能已经被删除或挪走。
func LooksLikeHome(home string) bool {
	p := Paths{Home: home}
	if _, err := os.Stat(p.Index()); err == nil {
		return true
	}
	if st, err := os.Stat(p.Sessions()); err == nil && st.IsDir() {
		return true
	}
	return false
}

// Probe 探测所有候选，只保留有效的（存在索引或会话目录），按会话数降序
func Probe(remembered string) []Candidate {
	var out []Candidate
	for _, raw := range RawCandidates(remembered) {
		c := inspect(raw)
		if c.Valid {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SessionCount != out[j].SessionCount {
			return out[i].SessionCount > out[j].SessionCount
		}
		return out[i].Home < out[j].Home
	})
	return out
}

// inspect 检查单个候选目录的实际内容
func inspect(raw RawCandidate) Candidate {
	p := Paths{Home: raw.Home}
	c := Candidate{Home: raw.Home, Source: raw.Source}

	if _, err := os.Stat(p.Index()); err == nil {
		c.HasIndex = true
		c.Valid = true
	}
	if st, err := os.Stat(p.Sessions()); err == nil && st.IsDir() {
		c.Valid = true
		c.SessionCount = CountSessions(p.Sessions())
	}
	if c.Valid {
		c.SizeBytes = DirSize(raw.Home)
	}
	return c
}

// CountSessions 统计会话正文文件数量，只走两层目录，避免深递归
func CountSessions(sessionsDir string) int {
	n := 0
	wsEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0
	}
	for _, ws := range wsEntries {
		if !ws.IsDir() {
			continue
		}
		sub, err := os.ReadDir(filepath.Join(sessionsDir, ws.Name()))
		if err != nil {
			continue
		}
		for _, s := range sub {
			if !s.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(sessionsDir, ws.Name(), s.Name(), SessionFile)); err == nil {
				n++
			}
		}
	}
	return n
}

// DirSize 递归统计目录占用
func DirSize(dir string) int64 {
	var total int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
