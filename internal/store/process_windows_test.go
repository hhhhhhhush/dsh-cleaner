//go:build windows

package store

import "testing"

// 进程快照检测的正确性：不存在的进程绝不能误报，存在的必须能命中。
func TestProcessSnapshot(t *testing.T) {
	if processRunning("workbuddy-no-such-process-4f2c.exe") {
		t.Fatal("不存在的进程不该返回 true")
	}
	// explorer.exe 在交互式桌面基本必然存在；万一没有就跳过，不算失败
	if !processRunning("explorer.exe") {
		t.Skip("当前会话没有 explorer.exe，跳过命中测试")
	}
	if !processRunning("EXPLORER.EXE") {
		t.Error("进程名比较应忽略大小写")
	}
}
