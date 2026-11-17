package ui

import (
	"fmt"
	"time"
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

// describeToken 返回 access_token 状态的可读描述（用于日志 / 状态栏 / 设置界面）。
func describeToken(token string, expiredAt time.Time, canAuto bool) string {
	switch {
	case token == "" && canAuto:
		return "access_token：未获取（已配置 clientID/clientSecret，将自动获取）"
	case token == "":
		return "access_token：未配置（请填写 access_token 或 clientID/clientSecret）"
	case expiredAt.IsZero():
		return "access_token：已配置（有效期未知，接口返回 401 时会自动重试）"
	case time.Now().After(expiredAt):
		return "access_token：已过期（" + expiredAt.Local().Format("2006-01-02 15:04") + "）"
	case time.Until(expiredAt) <= 5*time.Minute:
		return "access_token：即将过期（" + expiredAt.Local().Format("2006-01-02 15:04") + "），会自动重新获取"
	default:
		return fmt.Sprintf("access_token：有效期至 %s（剩余 %s）",
			expiredAt.Local().Format("2006-01-02 15:04"), humanDuration(time.Until(expiredAt)))
	}
}

// humanDuration 把时长格式化为「x 天 y 小时」之类的短文本。
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, mins)
	default:
		return fmt.Sprintf("%d 分钟", mins)
	}
}
