package ui

import (
	"fmt"
	"time"

	"pan123/internal/store"
)

// FormatSize 把字节数格式化为易读字符串。
func FormatSize(n int64) string {
	const unit = 1024
	if n < 0 {
		return "-"
	}
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// formatTime 格式化时间，零值显示为 "-"。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// displayDir 把 WebDAV 路径转成展示路径，例如 "/电影/2024" -> "我的云盘/电影/2024"。
func displayDir(p string) string {
	p = store.NormalizePath(p)
	if p == "/" {
		return "我的云盘"
	}
	return "我的云盘" + p
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}
