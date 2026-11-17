package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"pan123/internal/config"
)

// onSettings 打开设置对话框（鉴权、QPS、分页大小）。
func (a *App) onSettings() {
	var dlg *walk.Dialog
	var tokenEdit, cidEdit, secretEdit *walk.LineEdit
	var qpsEdit, pageEdit *walk.NumberEdit

	old := *a.cfg

	save := func() {
		a.cfg.AccessToken = strings.TrimSpace(tokenEdit.Text())
		a.cfg.ClientID = strings.TrimSpace(cidEdit.Text())
		a.cfg.ClientSecret = strings.TrimSpace(secretEdit.Text())
		a.cfg.QPS = qpsEdit.Value()
		a.cfg.PageSize = int(pageEdit.Value() + 0.5)

		if a.cfg.AccessToken != old.AccessToken || a.cfg.AccessToken == "" {
			a.cfg.TokenExpiredAt = time.Time{}
		}
		a.cfg.Sanitize()

		if err := a.cfg.Save(a.cfgPath); err != nil {
			walk.MsgBox(dlg, "保存失败", err.Error(), walk.MsgBoxIconError)
			return
		}

		a.client.Configure(a.clientOptions())
		qps := a.cfg.QPS
		a.logf("设置已保存：QPS=%.1f，每页 %d 条，鉴权方式：%s", qps, a.cfg.PageSize, authMode(a.cfg))
		dlg.Accept()

		// 用新配置做一次鉴权检查（在后台，不阻塞 UI）。
		go func() {
			if !a.client.HasCredentials() {
				a.setInfo("未配置 access_token")
				return
			}
			if err := a.client.EnsureToken(context.Background()); err != nil {
				a.logf("鉴权检查失败：%v", err)
				a.setInfo("鉴权失败，请检查设置")
				return
			}
			a.logf("鉴权检查通过")
			a.setInfo("鉴权正常 · QPS " + trimFloat(qps))
		}()
	}

	err := Dialog{
		AssignTo: &dlg,
		Title:    "设置",
		Size:     Size{660, 420},
		MinSize:  Size{560, 380},
		Layout: VBox{
			Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10},
			Spacing: 8,
		},
		Children: []Widget{
			GroupBox{
				Title:  "鉴权（两种方式二选一；填写 clientID/clientSecret 可自动换取 access_token）",
				Layout: VBox{Spacing: 4},
				Children: []Widget{
					Label{Text: "access_token（123 云盘开放平台后台生成）："},
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
			VSpacer{},
			Composite{
				Layout: HBox{Spacing: 8},
				Children: []Widget{
					HSpacer{},
					PushButton{Text: "取消", MinSize: Size{80, 0}, OnClicked: func() { dlg.Cancel() }},
					PushButton{Text: "保存", MinSize: Size{80, 0}, OnClicked: save},
				},
			},
		},
	}.Create(a.mw)
	if err != nil {
		walk.MsgBox(a.mw, "打开设置失败", err.Error(), walk.MsgBoxIconError)
		return
	}

	dlg.Run()
}

func authMode(c *config.Config) string {
	switch {
	case c.AccessToken != "":
		return "access_token"
	case c.ClientID != "" && c.ClientSecret != "":
		return "clientID/clientSecret（自动换取 token）"
	default:
		return "未配置"
	}
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
