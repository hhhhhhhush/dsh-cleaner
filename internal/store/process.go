// 客户端进程检测：客户端运行时索引会被内存态覆盖，必须拒绝执行。
//
// 具体探测按平台拆到 process_windows.go / process_other.go：
// Windows 走进程快照 API，不起子进程。早期版本用 tasklist，界面每 5 秒心跳
// 都会拉起一个控制台子进程，屏幕上就会不停闪黑框。
package store

import "dsh-cleaner/internal/config"

// checkProcess 当前使用的检测函数。抽成变量只为单元测试能绕开真实进程检查，
// 生产路径永远是 processRunning，不会从界面或接口被改动。
var checkProcess = processRunning

// IsRunning 返回客户端是否正在运行，以及命中的进程名
func IsRunning() (bool, string) {
	for _, name := range config.ProcessNames {
		if checkProcess(name) {
			return true, name
		}
	}
	return false, ""
}
