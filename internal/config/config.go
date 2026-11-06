// Package config 集中所有常量与路径推导规则。
// 换平台、换数据目录结构时只改这里。
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// 数据目录内的固定结构，来自官方 @deepseek-ai/dsh-home-paths 与实测
const (
	HomeEnv       = "DSH_HOME"              // 官方环境变量，优先级最高
	SessionFile   = "session.v4.jsonl.zstd" // 会话正文文件名
	SessionsDir   = "sessions"              // <home>/sessions/<工作区>/session-<uuid>/
	StoragesDir   = "storages"              // <home>/storages/workspace.json 索引
	IndexName     = "workspace.json"        // 工作区/归档/置顶索引
	TrashDirName  = "trash"                 // 隔离区（本工具自己的目录也在这里）
	BackupDirName = "backup-dsh-cleaner"    // 索引备份
	AppConfigDir  = "dsh-cleaner"           // 用户配置存放处（记住选择的数据目录）
	AppConfigFile = "config.json"
	TrashPrefix   = "dsh-cleaner-" // 隔离区子目录前缀
)

// 客户端进程名，运行中禁止改索引（会被内存态覆盖）
var ProcessNames = []string{
	"DeepSeek Harness.exe",
	"DeepSeek Harness",
	"dsh",
}

// Paths 描述一个候选数据根目录下的各个位置
type Paths struct {
	Home string
}

func (p Paths) Sessions() string { return filepath.Join(p.Home, SessionsDir) }
func (p Paths) Storages() string { return filepath.Join(p.Home, StoragesDir) }
func (p Paths) Index() string    { return filepath.Join(p.Home, StoragesDir, IndexName) }
func (p Paths) Trash() string    { return filepath.Join(p.Home, TrashDirName) }
func (p Paths) Backup() string   { return filepath.Join(p.Home, BackupDirName) }

// UserHome 返回操作系统用户主目录
func UserHome() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// ConfigPath 本工具自己的配置文件位置（不放进 DSH 数据目录，避免被误删）
func ConfigPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = UserHome()
	}
	return filepath.Join(base, AppConfigDir, AppConfigFile)
}

// CacheDir 本工具的缓存目录：浏览器 profile、临时文件放这里。
// 用独立 profile 有两个好处：窗口尺寸不会被上一次的窗口状态覆盖，
// 也不受用户自己浏览器里的扩展、代理、深色模式影响。
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = UserHome()
	}
	return filepath.Join(base, AppConfigDir)
}

// Drives 返回需要扫描的盘符根（仅 Windows 有意义）
func Drives() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	var out []string
	for c := 'C'; c <= 'Z'; c++ {
		root := string(c) + ":\\"
		if _, err := os.Stat(root); err == nil {
			out = append(out, root)
		}
	}
	return out
}
