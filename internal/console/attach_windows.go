//go:build windows

// Package console 让 windowsgui 模式下的程序也能从命令行看到输出。
// 判断顺序：已有可用的 stdout（管道、重定向、控制台）就原样保留；
// 只有句柄无效时才尝试附加父控制台。双击运行时没有父控制台，附加失败即静默跳过。
package console

import (
	"os"
	"syscall"
)

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	procAttach    = kernel32.NewProc("AttachConsole")
	procGetHandle = kernel32.NewProc("GetStdHandle")
)

const (
	attachParentProcess = ^uintptr(0) // (DWORD)-1
	stdOutputHandle     = 0xFFFFFFF5  // (DWORD)-11
	invalidHandle       = ^uintptr(0) // INVALID_HANDLE_VALUE
)

// Attach 确保标准输出可用；返回 true 表示有可写输出
func Attach() bool {
	if handleValid() {
		return true
	}
	if r, _, _ := procAttach.Call(attachParentProcess); r == 0 {
		return false
	}
	out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	os.Stdout = out
	os.Stderr = out
	return true
}

// handleValid 判断当前 stdout 是否是可写句柄（管道、文件、控制台都算）
func handleValid() bool {
	h, _, _ := procGetHandle.Call(uintptr(stdOutputHandle))
	return h != 0 && h != invalidHandle
}

// HasConsole 与 Attach 同义，供调用方按语义选择
func HasConsole() bool { return Attach() }
