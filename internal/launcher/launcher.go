// Package launcher 用系统已有的浏览器以「应用模式」打开界面。
// 应用模式没有地址栏、标签页和菜单，和原生窗口几乎一样，且不额外占体积。
package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"dsh-cleaner/internal/config"
)

var (
	// Windows 上常见的 Chromium 系浏览器位置
	winBrowsers = []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	macBrowsers = []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	}
)

// Open 打开界面。返回错误时调用方应回退到提示用户手动访问。
func Open(url string) error {
	switch runtime.GOOS {
	case "windows":
		return openWindows(url)
	case "darwin":
		return openDarwin(url)
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func openWindows(url string) error {
	// 独立的浏览器 profile：窗口尺寸才不会被 Edge 记住的上次状态覆盖，
	// 也不会被用户自己的扩展、代理、主题影响。
	profile := filepath.Join(config.CacheDir(), "browser")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		profile = ""
	}

	for _, exe := range winBrowsers {
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		args := []string{
			"--app=" + url,
			"--window-size=1300,900",
			"--window-position=60,36",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-features=Translate",
		}
		if profile != "" {
			args = append(args, "--user-data-dir="+profile)
		}
		cmd := exec.Command(exe, args...)
		if err := cmd.Start(); err == nil {
			go cmd.Wait() // 回收子进程，避免僵尸
			return nil
		}
	}
	// 没有 Chromium 系浏览器时退回默认浏览器（会有地址栏，但能用）
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	return cmd.Start()
}

func openDarwin(url string) error {
	for _, exe := range macBrowsers {
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		cmd := exec.Command(exe, "--app="+url, "--window-size=1180,820")
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		}
	}
	return exec.Command("open", url).Start()
}

// BrowserName 返回实际会使用的浏览器，用于界面提示
func BrowserName() string {
	switch runtime.GOOS {
	case "windows":
		for _, exe := range winBrowsers {
			if _, err := os.Stat(exe); err == nil {
				return filepath.Base(exe)
			}
		}
		return "系统默认浏览器"
	case "darwin":
		for _, exe := range macBrowsers {
			if _, err := os.Stat(exe); err == nil {
				return filepath.Base(exe)
			}
		}
		return "系统默认浏览器"
	default:
		return "系统默认浏览器"
	}
}
