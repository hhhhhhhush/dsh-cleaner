//go:build !windows

// Package console 在非 Windows 平台无需附加控制台，行为恒为成功。
package console

// Attach 非 Windows 平台始终有 stdout，直接返回 true
func Attach() bool { return true }

// HasConsole 非 Windows 平台始终有 stdout
func HasConsole() bool { return true }
