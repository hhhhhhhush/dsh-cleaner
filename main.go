// dsh-cleaner：DeepSeek Harness 会话清理工具（非官方）
// 用法：双击直接打开界面；带参数则进入命令行模式。
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dsh-cleaner/internal/config"
	"dsh-cleaner/internal/console"
	"dsh-cleaner/internal/launcher"
	"dsh-cleaner/internal/server"
	"dsh-cleaner/internal/store"
)

// version 由构建脚本通过 -ldflags "-X main.version=..." 注入
var version = "0.1.0"

// idleTimeout 界面无心跳多久后自动退出（秒）。关掉窗口后进程会自动清理，不残留。
// 不能设太短：浏览器会把后台窗口的定时器节流到一分钟一次，窗口最小化或系统休眠恢复时
// 心跳可能延迟，阈值太紧会导致界面还开着、后台服务已经退了。
const idleTimeout = 300

func main() {
	var (
		homeFlag  = flag.String("home", "", "指定 DSH 数据目录")
		noBrowser = flag.Bool("no-browser", false, "只启动本地服务，不自动打开窗口")
		showVer   = flag.Bool("version", false, "显示版本")
		listMode  = flag.Bool("list", false, "列出全部会话后退出")
		delArch   = flag.Bool("delete-archived", false, "删除全部已归档会话")
		delOrphan = flag.Bool("delete-orphan", false, "清空未分组会话")
		hardFlag  = flag.Bool("hard", false, "命令行删除时直接从磁盘抹除，不进隔离区")
		wsID      = flag.String("delete-workspace", "", "删除指定工作区（按 ID）")
	)
	flag.Parse()

	setupLog()
	console.Attach() // 从命令行启动时让输出可见；双击启动时静默跳过
	if *showVer {
		fmt.Println("dsh-cleaner", version)
		return
	}

	paths, err := resolveHome(*homeFlag)
	if err != nil {
		fatal(err)
		return
	}

	if *listMode || *delArch || *delOrphan || *wsID != "" {
		runCLI(paths, *listMode, *delArch, *delOrphan, *wsID, *hardFlag)
		return
	}

	runGUI(paths, *noBrowser)
}

// setupLog 把日志落到文件，双击闪退时能查原因
func setupLog() {
	dir := filepath.Dir(config.ConfigPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "dsh-cleaner.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.Printf("---- 启动 v%s ----", version)
}

// resolveHome 决定使用哪个数据目录：
// 优先 --home，其次上次记住的，最后取会话最多的候选
func resolveHome(explicit string) (config.Paths, error) {
	if explicit != "" {
		return config.Paths{Home: filepath.Clean(explicit)}, nil
	}
	// 记住的目录可能已经被删掉或挪走，先确认索引还在，否则回退到重新探测
	if prof := config.LoadProfile(); prof.Home != "" && config.LooksLikeHome(prof.Home) {
		return config.Paths{Home: prof.Home}, nil
	}
	cands := config.Probe("")
	if len(cands) == 0 {
		return config.Paths{}, errors.New("没有找到 DSH 数据目录，请用 --home 指定（例如 --home C:\\Users\\你\\.dsh）")
	}
	return config.Paths{Home: cands[0].Home}, nil
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func runGUI(paths config.Paths, noBrowser bool) {
	token := randomToken()
	srv := server.New(paths, token)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(fmt.Errorf("本地端口监听失败: %w", err))
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, token)
	log.Printf("服务已启动 %s（数据目录 %s）", url, paths.Home)

	go func() {
		if err := http.Serve(ln, srv.Handler()); err != nil {
			log.Printf("服务退出: %v", err)
		}
	}()

	if noBrowser {
		fmt.Printf("界面地址：%s\n数据目录：%s\n按 Ctrl+C 结束。\n", url, paths.Home)
	} else if err := launcher.Open(url); err != nil {
		// 双击启动时没有控制台，必须弹窗告诉用户，否则就是"双击没反应"
		console.Alert("无法打开界面",
			"没能自动打开浏览器窗口。\n\n请手动复制下面的地址到浏览器打开：\n"+url+"\n\n错误："+err.Error())
	}

	// 界面关掉后不再有心跳，这里负责退出，避免后台残留进程
	for {
		time.Sleep(5 * time.Second)
		if srv.IdleSeconds() > idleTimeout {
			log.Printf("界面 %d 秒无活动，退出", srv.IdleSeconds())
			return
		}
	}
}

func runCLI(paths config.Paths, list, delArch, delOrphan bool, wsID string, hard bool) {
	switch {
	case list:
		snap, err := store.Scan(paths)
		if err != nil {
			fatal(err)
			return
		}
		fmt.Printf("数据目录：%s\n共 %d 个会话\n\n", snap.Home, len(snap.Sessions))
		for _, s := range snap.Sessions {
			tag := ""
			if s.Archived {
				tag += " [已归档]"
			}
			if !s.InIndex {
				tag += " [工作区已删]"
			}
			fmt.Printf("%s  %s%s\n", padRight(truncate(s.Title, 40), 44), s.WorkspacePath, tag)
		}
	case delArch:
		reportDelete(store.RemoveArchived(paths, hard))
	case delOrphan:
		reportDelete(store.RemoveOrphans(paths, hard))
	case wsID != "":
		reportDelete(store.RemoveWorkspace(paths, wsID, hard))
	}
}

func reportDelete(res *store.RemoveResult, err error) {
	if err != nil {
		fatal(err)
		return
	}
	fmt.Printf("已处理 %d 个会话（%s）\n", res.Removed, res.Mode)
	if res.TrashDir != "" {
		fmt.Printf("隔离区：%s\n", res.TrashDir)
	}
	if res.Backup != "" {
		fmt.Printf("索引备份：%s\n", res.Backup)
	}
	if len(res.Failed) > 0 {
		fmt.Printf("失败 %d 个：\n", len(res.Failed))
		for _, f := range res.Failed {
			fmt.Println("  " + f)
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// padRight 按终端显示宽度补空格，中文按 2 列算，避免列表错位
func padRight(s string, width int) string {
	w := 0
	for _, r := range s {
		if r > 0x7F {
			w += 2
		} else {
			w++
		}
	}
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func fatal(err error) {
	log.Printf("错误: %v", err)
	// 双击启动（无控制台）时用户看不到 stderr，必须弹窗
	if !console.HasConsole() {
		console.Alert("DSH Cleaner 无法启动", err.Error())
		return
	}
	fmt.Fprintf(os.Stderr, "错误：%v\n", err)
}
