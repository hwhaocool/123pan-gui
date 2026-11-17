package ui

import "fmt"

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
