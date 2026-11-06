//go:build !windows

package store

import (
	"os/exec"
	"strings"
)

// processRunning 在 macOS / Linux 上用 pgrep。这些平台起子进程不会弹窗，无需特殊处理。
func processRunning(name string) bool {
	out, err := exec.Command("pgrep", "-f", name).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
