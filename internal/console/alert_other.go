//go:build !windows

package console

import "log"

// Alert 非 Windows 平台没有弹窗，写日志即可
func Alert(title, message string) {
	log.Printf("[%s] %s", title, message)
}
