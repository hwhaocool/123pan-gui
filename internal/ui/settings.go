package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"pan123/internal/config"
	"pan123/internal/webdav"
)

// settingsDraft 是设置页正在编辑的值（保存前不影响运行中的配置）。
type settingsDraft struct {
	url      string
	user     string
	pass     string
	insecure bool
	qps      string
}

func (a *App) openSettings() {
	a.draft = settingsDraft{
		url:      a.cfg.WebDAVURL,
		user:     a.cfg.Username,
		pass:     a.cfg.Password,
		insecure: a.cfg.InsecureTLS,
		qps:      strconv.FormatFloat(a.cfg.QPS, 'f', -1, 64),
	}
	a.settingsStatus = describeWebDAV(a.cfg)
	a.settingsTesting = false
	a.settingsOpen = true
}

func (a *App) closeSettings() {
	a.settingsOpen = false
	a.settingsTesting = false
}

func (a *App) settingsView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Fill().Padding(22).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "设置").FontSize(20).Bold()
			ui.Spacer(c)
			if ui.Button(c, "返回").Clicked() {
				a.closeSettings()
			}
		})

		ui.Text(c, "WebDAV 根地址").TextColor(t.TextMuted)
		ui.TextInput(c, &a.draft.url).
			Placeholder("https://webdav.123pan.cn/webdav").
			Label("WebDAV 地址")

		ui.Row(c).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "用户名").TextColor(t.TextMuted)
				ui.TextInput(c, &a.draft.user).Label("用户名")
			})
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "密码").TextColor(t.TextMuted)
				ui.TextInput(c, &a.draft.pass).Password().Label("密码")
			})
		})

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, &a.draft.insecure, "忽略 HTTPS 证书错误（自建 / 内网常用）")
			ui.Spacer(c)
			ui.Text(c, "QPS 限速").TextColor(t.TextMuted)
			ui.TextInput(c, &a.draft.qps).Width(80).Label("QPS")
		})

		ui.Text(c, "123 云盘官方 WebDAV：https://webdav.123pan.cn/webdav（用户名/密码见官方 WebDAV 文档）").
			TextColor(t.TextMuted)

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "填入 123 云盘地址").Clicked() {
				a.draft.url = config.DefaultWebDAVURL
			}
			if ui.Button(c, "测试连接").Disabled(a.settingsTesting).Clicked() {
				a.testConnection()
			}
			ui.Spacer(c)
			if ui.Button(c, "取消").Clicked() {
				a.closeSettings()
			}
			if ui.PrimaryButton(c, "保存").Clicked() {
				a.saveSettings()
			}
		})

		if a.settingsStatus != "" {
			ui.Text(c, a.settingsStatus).TextColor(t.TextMuted)
		}
	})
}

func (a *App) testConnection() {
	opts := webdav.Options{
		BaseURL:     strings.TrimSpace(a.draft.url),
		Username:    strings.TrimSpace(a.draft.user),
		Password:    a.draft.pass,
		InsecureTLS: a.draft.insecure,
		QPS:         a.cfg.QPS,
	}
	if opts.BaseURL == "" {
		a.settingsStatus = "请先填写 WebDAV 地址"
		return
	}

	a.settingsTesting = true
	a.settingsStatus = "正在连接 " + opts.BaseURL + " …"
	go func() {
		probe := webdav.New(opts)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		err := probe.Check(ctx)
		a.async(func() {
			a.settingsTesting = false
			if err != nil {
				a.settingsStatus = "连接失败：" + err.Error()
				return
			}
			a.settingsStatus = "连接成功 · " + opts.BaseURL
		})
	}()
}

func (a *App) saveSettings() {
	qps, ok := a.parseQPS(a.draft.qps)
	if !ok {
		a.settingsStatus = "QPS 需要是大于 0 的数字"
		return
	}

	cfg := *a.cfg
	cfg.WebDAVURL = strings.TrimSpace(a.draft.url)
	cfg.Username = strings.TrimSpace(a.draft.user)
	cfg.Password = a.draft.pass
	cfg.InsecureTLS = a.draft.insecure
	cfg.QPS = qps
	cfg.Sanitize()

	if err := cfg.Save(a.cfgPath); err != nil {
		a.settingsStatus = "保存失败：" + err.Error()
		return
	}

	a.cfg = &cfg
	a.client.Configure(a.clientOptions())
	a.settingsOpen = false
	a.settingsTesting = false
	a.addLog("设置已保存：WebDAV=%s，QPS=%.1f", a.cfg.WebDAVURL, a.cfg.QPS)
	a.info = fmt.Sprintf("QPS %.1f", a.cfg.QPS)
	go a.checkConnection()
}

func (a *App) checkConnection() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := a.client.Check(ctx); err != nil {
		a.async(func() {
			a.addLog("WebDAV 连接失败：%v", err)
			if webdav.IsAuthError(err) {
				a.addLog("提示：123 云盘 WebDAV 请使用官方文档中的 WebDAV 用户名/密码；账号未开通或无权限时也会返回 401。")
			}
			a.info = "WebDAV 连接失败（仍可浏览本地缓存）"
			a.status = "连接失败"
		})
		return
	}

	a.async(func() {
		a.addLog("WebDAV 连接正常：%s", a.client.BaseURL())
		a.info = fmt.Sprintf("已连接 · QPS %.1f", a.client.QPS())
		a.status = "就绪"
	})
}

// describeWebDAV 返回当前 WebDAV 配置的可读描述。
func describeWebDAV(c *config.Config) string {
	if strings.TrimSpace(c.WebDAVURL) == "" {
		return "当前：尚未配置 WebDAV 地址"
	}
	auth := "匿名"
	if c.Username != "" {
		auth = "Basic Auth：" + c.Username
	}
	if strings.EqualFold(strings.TrimRight(c.WebDAVURL, "/"), config.DefaultWebDAVURL) {
		return fmt.Sprintf("当前：123 云盘官方 WebDAV（%s）", auth)
	}
	return fmt.Sprintf("当前：%s（%s）", c.WebDAVURL, auth)
}
