package ui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"pan123/internal/config"
	"pan123/internal/webdav"
)

// onSettings 打开设置对话框（WebDAV 地址/账号、QPS）。
func (a *App) onSettings() {
	var (
		dlg         *walk.Dialog
		urlEdit     *walk.LineEdit
		userEdit    *walk.LineEdit
		passEdit    *walk.LineEdit
		qpsEdit     *walk.NumberEdit
		insecure    *walk.CheckBox
		statusLabel *walk.Label
	)

	old := *a.cfg
	var closed atomic.Bool

	formOptions := func() webdav.Options {
		return webdav.Options{
			BaseURL:     strings.TrimSpace(urlEdit.Text()),
			Username:    strings.TrimSpace(userEdit.Text()),
			Password:    passEdit.Text(),
			InsecureTLS: insecure.Checked(),
			QPS:         qpsEdit.Value(),
		}
	}

	// apply 把界面上的值写回配置并保存。
	apply := func() bool {
		opts := formOptions()

		a.cfg.WebDAVURL = strings.TrimSpace(opts.BaseURL)
		a.cfg.Username = strings.TrimSpace(opts.Username)
		a.cfg.Password = opts.Password
		a.cfg.InsecureTLS = opts.InsecureTLS
		a.cfg.QPS = opts.QPS
		a.cfg.Sanitize()

		if err := a.cfg.Save(a.cfgPath); err != nil {
			walk.MsgBox(dlg, "保存失败", err.Error(), walk.MsgBoxIconError)
			return false
		}

		a.client.Configure(a.clientOptions())
		a.logf("设置已保存：WebDAV=%s，QPS=%.1f", a.cfg.WebDAVURL, a.cfg.QPS)
		statusLabel.SetText(describeWebDAV(a.cfg))
		return true
	}

	save := func() {
		if !apply() {
			return
		}
		closed.Store(true)
		dlg.Accept()

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			if err := a.client.Check(ctx); err != nil {
				a.logf("WebDAV 连接检查失败：%v", err)
				a.setInfo("WebDAV 连接失败（仍可浏览本地缓存）")
				return
			}
			a.logf("WebDAV 连接正常：%s", a.client.BaseURL())
			a.setInfo(fmt.Sprintf("已连接 · QPS %.1f", a.client.QPS()))
		}()
	}

	// testConn 用界面上的当前值（未保存也可以）测试连接。
	testConn := func() {
		opts := formOptions()
		if opts.BaseURL == "" {
			statusLabel.SetText("请先填写 WebDAV 地址")
			return
		}

		statusLabel.SetText("正在连接 " + opts.BaseURL + " …")
		go func() {
			probe := webdav.New(opts)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			err := probe.Check(ctx)
			if closed.Load() {
				return
			}
			dlg.Synchronize(func() {
				if closed.Load() {
					return
				}
				if err != nil {
					statusLabel.SetText("连接失败：" + err.Error())
					return
				}
				statusLabel.SetText("连接成功 · " + opts.BaseURL)
			})
			if err != nil {
				a.logf("WebDAV 连接测试失败：%v", err)
				return
			}
			a.logf("WebDAV 连接测试成功：%s", opts.BaseURL)
		}()
	}

	err := Dialog{
		AssignTo: &dlg,
		Title:    "设置",
		Size:     Size{760, 480},
		MinSize:  Size{640, 420},
		Layout: VBox{
			Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10},
			Spacing: 8,
		},
		Children: []Widget{
			GroupBox{
				Title:  "WebDAV",
				Layout: VBox{Spacing: 4},
				Children: []Widget{
					Label{Text: "WebDAV 根地址（例如 https://dav.example.com/dav/123pan）："},
					LineEdit{AssignTo: &urlEdit, Text: old.WebDAVURL},
					Composite{
						Layout: HBox{Spacing: 6},
						Children: []Widget{
							Label{Text: "用户名：", MinSize: Size{60, 0}},
							LineEdit{AssignTo: &userEdit, Text: old.Username},
							Label{Text: "密码：", MinSize: Size{45, 0}},
							LineEdit{AssignTo: &passEdit, Text: old.Password, PasswordMode: true},
						},
					},
					CheckBox{
						AssignTo: &insecure,
						Text:     "忽略 HTTPS 证书错误（自建 / 内网 WebDAV 常用）",
						Checked:  old.InsecureTLS,
					},
					Label{
						AssignTo: &statusLabel,
						Text:     describeWebDAV(&old),
					},
				},
			},
			GroupBox{
				Title:  "同步参数",
				Layout: HBox{Spacing: 8},
				Children: []Widget{
					Label{Text: "QPS 限速：", MinSize: Size{80, 0}},
					NumberEdit{
						AssignTo:  &qpsEdit,
						Value:     old.QPS,
						Decimals:  1,
						MinValue:  0.2,
						MaxValue:  50,
						Increment: 1,
						MinSize:   Size{70, 0},
					},
					Label{Text: "（每秒请求上限，所有同步请求都会串行排队；WebDAV 无分页）"},
					HSpacer{},
				},
			},
			Label{Text: "说明：123 云盘暂未提供官方 WebDAV 入口，可通过 WebDAV 网关（如 Alist 等）挂载 123 云盘后再填写其地址。"},
			VSpacer{},
			Composite{
				Layout: HBox{Spacing: 8},
				Children: []Widget{
					PushButton{Text: "测试连接", MinSize: Size{100, 0}, OnClicked: testConn},
					HSpacer{},
					PushButton{Text: "取消", MinSize: Size{80, 0}, OnClicked: func() {
						closed.Store(true)
						dlg.Cancel()
					}},
					PushButton{Text: "保存", MinSize: Size{80, 0}, OnClicked: save},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, "打开设置失败", err.Error(), walk.MsgBoxIconError)
		return
	}

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) { closed.Store(true) })
	dlg.Run()
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
	return fmt.Sprintf("当前：%s（%s）", c.WebDAVURL, auth)
}
