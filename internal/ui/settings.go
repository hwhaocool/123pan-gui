package ui

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"pan123/internal/config"
)

// onSettings 打开设置对话框（鉴权、token 自动获取、QPS、分页大小）。
func (a *App) onSettings() {
	var (
		dlg         *walk.Dialog
		tokenEdit   *walk.LineEdit
		cidEdit     *walk.LineEdit
		secretEdit  *walk.LineEdit
		qpsEdit     *walk.NumberEdit
		pageEdit    *walk.NumberEdit
		statusLabel *walk.Label
	)

	old := *a.cfg
	lastToken := strings.TrimSpace(old.AccessToken)

	var closed atomic.Bool

	// apply 把界面上的值写回配置并保存（不发请求）。
	apply := func() bool {
		fieldToken := strings.TrimSpace(tokenEdit.Text())

		a.cfg.ClientID = strings.TrimSpace(cidEdit.Text())
		a.cfg.ClientSecret = strings.TrimSpace(secretEdit.Text())
		a.cfg.QPS = qpsEdit.Value()
		a.cfg.PageSize = int(pageEdit.Value() + 0.5)

		switch {
		case fieldToken != lastToken:
			// 用户手动改过 access_token（可能是粘贴新的，也可能是清空）。
			a.cfg.AccessToken = fieldToken
			a.cfg.TokenExpiredAt = time.Time{}
			lastToken = fieldToken
		default:
			// 输入框没动过：以客户端当前 token 为准（可能刚被自动获取/续期）。
			if tok, exp := a.client.TokenState(); tok != "" {
				a.cfg.AccessToken = tok
				a.cfg.TokenExpiredAt = exp
			}
		}

		a.cfg.Sanitize()

		if err := a.cfg.Save(a.cfgPath); err != nil {
			walk.MsgBox(dlg, "保存失败", err.Error(), walk.MsgBoxIconError)
			return false
		}

		a.client.Configure(a.clientOptions())
		a.logf("设置已保存：QPS=%.1f，每页 %d 条，鉴权方式：%s", a.cfg.QPS, a.cfg.PageSize, authMode(a.cfg))
		statusLabel.SetText(describeToken(a.cfg.AccessToken, a.cfg.TokenExpiredAt, a.client.CanAutoRefresh()))
		return true
	}

	save := func() {
		if !apply() {
			return
		}
		closed.Store(true)
		dlg.Accept()
		go a.checkAuth(context.Background())
	}

	// fetchToken 保存配置后立刻用 clientID/clientSecret 换取 access_token。
	fetchToken := func() {
		if !apply() {
			return
		}
		if !a.client.CanAutoRefresh() {
			statusLabel.SetText("无法自动获取：请填写 clientID 和 clientSecret")
			return
		}

		statusLabel.SetText("正在获取 access_token…")
		go func() {
			expiredAt, err := a.client.RefreshToken(context.Background())
			token, exp := a.client.TokenState()
			if exp.IsZero() {
				exp = expiredAt
			}

			if !closed.Load() {
				dlg.Synchronize(func() {
					if closed.Load() {
						return
					}
					if err != nil {
						statusLabel.SetText("获取失败：" + err.Error())
						return
					}
					tokenEdit.SetText(token)
					lastToken = token
					statusLabel.SetText(describeToken(token, exp, true))
					a.setInfo("鉴权正常 · access_token 有效期至 " + exp.Local().Format("2006-01-02 15:04"))
				})
			}

			if err != nil {
				a.logf("获取 access_token 失败：%v", err)
				return
			}
			a.logf("已获取 access_token（有效期至 %s）", exp.Local().Format("2006-01-02 15:04:05"))
		}()
	}

	err := Dialog{
		AssignTo: &dlg,
		Title:    "设置",
		Size:     Size{720, 460},
		MinSize:  Size{620, 400},
		Layout: VBox{
			Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10},
			Spacing: 8,
		},
		Children: []Widget{
			GroupBox{
				Title:  "鉴权（access_token 可直接填，也可以留空由 clientID/clientSecret 自动获取）",
				Layout: VBox{Spacing: 4},
				Children: []Widget{
					Label{Text: "access_token（可留空；程序会在过期前 5 分钟自动重新获取）："},
					LineEdit{AssignTo: &tokenEdit, Text: old.AccessToken},
					Composite{
						Layout: HBox{Spacing: 6},
						Children: []Widget{
							Label{Text: "clientID：", MinSize: Size{70, 0}},
							LineEdit{AssignTo: &cidEdit, Text: old.ClientID},
							Label{Text: "clientSecret：", MinSize: Size{90, 0}},
							LineEdit{AssignTo: &secretEdit, Text: old.ClientSecret, PasswordMode: true},
						},
					},
					Label{
						AssignTo: &statusLabel,
						Text:     describeToken(old.AccessToken, old.TokenExpiredAt, old.ClientID != "" && old.ClientSecret != ""),
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
						MaxValue:  20,
						Increment: 0.1,
						MinSize:   Size{70, 0},
					},
					Label{Text: "每页条数（≤100）：", MinSize: Size{120, 0}},
					NumberEdit{
						AssignTo:  &pageEdit,
						Value:     float64(old.PageSize),
						Decimals:  0,
						MinValue:  1,
						MaxValue:  100,
						Increment: 10,
						MinSize:   Size{70, 0},
					},
					HSpacer{},
				},
			},
			Label{Text: "提示：123 云盘开放平台对单账号有 QPS 限制，建议 1~2。同步期间所有请求都经过同一个限速器串行发送，不会并发冲击接口。"},
			Label{Text: "自动获取 token 走 POST /api/v1/access_token，结果（accessToken + expiredAt）会写入 config.json。"},
			VSpacer{},
			Composite{
				Layout: HBox{Spacing: 8},
				Children: []Widget{
					PushButton{Text: "立即获取 access_token", MinSize: Size{160, 0}, OnClicked: fetchToken},
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

func authMode(c *config.Config) string {
	switch {
	case c.AccessToken != "":
		if c.ClientID != "" && c.ClientSecret != "" {
			return "access_token（已配置 clientID/clientSecret，过期可自动续期）"
		}
		return "access_token（手工填写，过期需手动更新）"
	case c.ClientID != "" && c.ClientSecret != "":
		return "clientID/clientSecret（自动获取 access_token）"
	default:
		return "未配置"
	}
}
