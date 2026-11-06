// 记住用户选定的数据目录，下次启动直接用它。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Profile 本工具的持久化设置
type Profile struct {
	Home          string `json:"home"`
	LastTrashView int64  `json:"lastTrashView"` // 预留：最近查看隔离区的时间
}

// LoadProfile 读取设置，文件不存在时返回空设置
func LoadProfile() Profile {
	var p Profile
	raw, err := os.ReadFile(ConfigPath())
	if err != nil {
		return p
	}
	json.Unmarshal(raw, &p)
	return p
}

// SaveProfile 写入设置，自动创建目录
func SaveProfile(p Profile) error {
	target := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, raw, 0o644)
}

// Remember 只更新数据目录，保留其他字段
func Remember(home string) error {
	p := LoadProfile()
	p.Home = home
	return SaveProfile(p)
}
