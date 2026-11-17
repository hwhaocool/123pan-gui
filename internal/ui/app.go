// Package ui 实现桌面界面（walk）。
package ui

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"pan123/internal/config"
	"pan123/internal/store"
	"pan123/internal/webdav"
)

// App 保存应用的运行状态。
type App struct {
	cfg     *config.Config
	cfgPath string

	cache  *store.Cache
	client *webdav.Client
	model  *TreeModel
	root   *Node
	syncer *Syncer

	mw         *walk.MainWindow
	tv         *walk.TreeView
	logEdit    *walk.TextEdit
	infoItem   *walk.StatusBarItem
	statusItem *walk.StatusBarItem

	syncBtn     *walk.PushButton
	syncAllBtn  *walk.PushButton
	stopBtn     *walk.PushButton
	settingsBtn *walk.PushButton
	subdirs     *walk.CheckBox

	closed   atomic.Bool
	logLines int
}

// Run 启动应用（阻塞直到窗口关闭）。
func Run() error {
	a := &App{}
	if err := a.init(); err != nil {
		return err
	}
	if err := a.build(); err != nil {
		return err
	}
	a.syncer.Start()

	a.mw.Starting().Attach(func() { go a.bootstrap() })
	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.closed.Store(true)
		a.syncer.Close()

		if t := a.cache.LastSyncedAt(); !t.IsZero() {
			a.cfg.LastSyncAt = t
		}
		if err := a.cfg.Save(a.cfgPath); err != nil {
			log.Printf("保存配置失败: %v", err)
		}
		if err := a.cache.Flush(); err != nil {
			log.Printf("合并数据库失败: %v", err)
		}
		if err := a.cache.Close(); err != nil {
			log.Printf("关闭数据库失败: %v", err)
		}
	})

	a.mw.Run()
	return nil
}

func (a *App) init() error {
	cfg, path, err := config.Load()
	if err != nil {
		log.Printf("加载配置: %v", err)
	}
	a.cfg, a.cfgPath = cfg, path

	cachePath, err := config.CachePath()
	if err != nil {
		return fmt.Errorf("无法确定数据库路径: %w", err)
	}
	a.cache, err = store.Open(cachePath)
	if err != nil {
		return fmt.Errorf("打开本地数据库失败: %w", err)
	}

	a.root = NewRootNode()
	a.model = NewTreeModel(a.root)

	a.client = webdav.New(a.clientOptions())
	a.client.OnWait = func(d time.Duration) {
		if d >= 2*time.Second {
			a.logf("触发限速，等待 %s …", d.Round(100*time.Millisecond))
		}
	}

	a.syncer = NewSyncer(a.client, a.cache)
	a.syncer.synchronize = func(f func()) {
		if a.mw != nil {
			a.mw.Synchronize(f)
		}
	}
	a.syncer.apply = func(n *Node, entries []store.Entry) { a.model.ApplyFolder(n, entries) }
	a.syncer.logf = a.logf
	a.syncer.status = a.setStatus
	a.syncer.busy = a.setBusy
	a.syncer.done = func(n *Node) {
		// 同步结束后展开该节点，让用户立刻看到结果。
		a.postUI(func() {
			if a.tv != nil {
				_ = a.tv.SetExpanded(n, true)
			}
		})
	}

	return nil
}

func (a *App) clientOptions() webdav.Options {
	c := a.cfg
	return webdav.Options{
		BaseURL:     c.WebDAVURL,
		Username:    c.Username,
		Password:    c.Password,
		InsecureTLS: c.InsecureTLS,
		QPS:         c.QPS,
	}
}

func (a *App) build() error {
	err := MainWindow{
		AssignTo: &a.mw,
		Title:    "123 云盘客户端（WebDAV）",
		Size:     Size{960, 680},
		MinSize:  Size{720, 480},
		Layout:   VBox{MarginsZero: true},
		Children: []Widget{
			Composite{
				Layout: HBox{
					Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 4},
					Spacing: 6,
				},
				Children: []Widget{
					PushButton{
						AssignTo:  &a.syncBtn,
						Text:      "同步选中目录",
						MinSize:   Size{110, 0},
						OnClicked: a.onSyncSelected,
					},
					PushButton{
						AssignTo:  &a.syncAllBtn,
						Text:      "同步全部",
						MinSize:   Size{90, 0},
						OnClicked: a.onSyncAll,
					},
					PushButton{
						AssignTo:  &a.stopBtn,
						Text:      "停止",
						Enabled:   false,
						MinSize:   Size{70, 0},
						OnClicked: a.onStop,
					},
					CheckBox{
						AssignTo: &a.subdirs,
						Text:     "包含子目录",
						Checked:  true,
					},
					HSpacer{},
					PushButton{
						AssignTo:  &a.settingsBtn,
						Text:      "设置…",
						MinSize:   Size{80, 0},
						OnClicked: a.onSettings,
					},
				},
			},
			VSplitter{
				Children: []Widget{
					TreeView{
						AssignTo:             &a.tv,
						Model:                a.model,
						MinSize:              Size{0, 220},
						OnExpandedChanged:    a.onExpanded,
						OnCurrentItemChanged: a.onSelectionChanged,
						OnItemActivated:      a.onItemActivated,
					},
					TextEdit{
						AssignTo: &a.logEdit,
						ReadOnly: true,
						HScroll:  true,
						VScroll:  true,
						MinSize:  Size{0, 110},
					},
				},
			},
		},
		StatusBarItems: []StatusBarItem{
			{AssignTo: &a.infoItem, Text: "就绪"},
			{AssignTo: &a.statusItem, Text: "", Width: 340},
		},
	}.Create()
	if err != nil {
		return err
	}

	a.setInfo("就绪")
	a.setStatus("")
	return nil
}

// bootstrap 在窗口显示后执行：读缓存 + 检查连接 + 首次自动同步。
func (a *App) bootstrap() {
	time.Sleep(150 * time.Millisecond)

	a.logf("123 云盘客户端已启动（WebDAV 模式）")
	a.logf("配置文件：%s", a.cfgPath)
	a.logf("数据库：%s", a.cache.Path())
	a.logf("限速：QPS=%.1f（两次请求最小间隔 %s），可在「设置…」中调整", a.client.QPS(), a.client.Interval())

	if folders, items := a.cache.Stats(); folders > 0 {
		a.logf("本地缓存：%d 个已同步目录 / %d 项，最近同步于 %s",
			folders, items, formatTime(a.cache.LastSyncedAt()))
	}

	rootCached := false
	if entries, ok := a.cache.Children("/"); ok {
		rootCached = true
		a.postUI(func() {
			a.model.ApplyFolder(a.root, entries)
			_ = a.tv.SetExpanded(a.root, true)
		})
		a.logf("已从本地数据库加载根目录（%d 项）", len(entries))
	}

	if !a.client.Configured() {
		a.logf("尚未配置 WebDAV 地址，请点击「设置…」填写后再同步")
		a.setInfo("未配置 WebDAV 地址")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	err := a.client.Check(ctx)
	cancel()
	if err != nil {
		a.logf("WebDAV 连接失败：%v", err)
		if webdav.IsAuthError(err) {
			a.logf("提示：123 云盘 WebDAV 请使用官方文档中的 WebDAV 用户名/密码；账号未开通或无权限时也会返回 401。")
		}
		a.setInfo("WebDAV 连接失败（仍可浏览本地缓存）")
		return
	}

	a.logf("WebDAV 连接正常：%s", a.client.BaseURL())
	a.setInfo(fmt.Sprintf("已连接 · QPS %.1f", a.client.QPS()))

	if !rootCached {
		a.logf("将自动同步根目录（仅第一层）…")
		if err := a.syncer.Submit(&Job{Node: a.root, Recursive: false, Reason: "startup"}); err != nil {
			a.logf("提交同步任务失败：%v", err)
		}
	}
}

func (a *App) selectedNode() *Node {
	if a.tv == nil {
		return nil
	}
	if n, ok := a.tv.CurrentItem().(*Node); ok {
		return n
	}
	return nil
}

func (a *App) onSyncSelected() {
	node := a.selectedNode()
	if node == nil {
		walk.MsgBox(a.mw, "提示", "请先在树中选中一个文件夹。", walk.MsgBoxIconInformation)
		return
	}
	a.submit(node, a.subdirs.Checked())
}

func (a *App) onSyncAll() {
	if !a.client.Configured() {
		walk.MsgBox(a.mw, "未配置 WebDAV", "请先在「设置…」中填写 WebDAV 地址（以及用户名/密码）。", walk.MsgBoxIconInformation)
		return
	}
	msg := "将递归同步整个 WebDAV 目录树。\n\n目录较多时可能需要较长时间，是否继续？"
	if walk.MsgBox(a.mw, "同步全部", msg, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	a.submit(a.root, true)
}

func (a *App) submit(node *Node, recursive bool) {
	if node == nil || !node.IsDir() {
		walk.MsgBox(a.mw, "提示", "只能同步文件夹。", walk.MsgBoxIconInformation)
		return
	}
	if !a.client.Configured() {
		walk.MsgBox(a.mw, "未配置 WebDAV", "请先在「设置…」中填写 WebDAV 地址（以及用户名/密码）。", walk.MsgBoxIconInformation)
		return
	}

	err := a.syncer.Submit(&Job{Node: node, Recursive: recursive, Reason: "manual"})
	if err != nil {
		a.logf("未能加入同步队列：%v", err)
		return
	}
	a.logf("已加入同步队列：%s（%s）", node.DisplayPath(), onOff(recursive, "含子目录", "仅本层"))
}

func (a *App) onStop() {
	a.syncer.Stop()
	a.statusf("正在停止…")
}

func (a *App) onExpanded(item walk.TreeItem) {
	node, ok := item.(*Node)
	if !ok || !node.IsDir() || a.tv == nil || !a.tv.Expanded(node) || node.Loaded() {
		return
	}

	// 优先使用本地数据库（离线可用，不消耗请求）。
	if entries, ok := a.cache.Children(node.Path()); ok {
		// 延后到当前消息处理完毕再刷新，避免在展开事件里重入 TreeView。
		a.postUI(func() {
			a.model.ApplyFolder(node, entries)
			_ = a.tv.SetExpanded(node, true)
		})
		syncedAt, _ := a.cache.SyncedAt(node.Path())
		a.logf("展开「%s」：从缓存加载 %d 项（缓存于 %s）", node.DisplayPath(), len(entries), formatTime(syncedAt))
		return
	}

	if !a.client.Configured() {
		return
	}

	a.logf("展开「%s」：没有缓存，自动同步一层…", node.DisplayPath())
	if err := a.syncer.Submit(&Job{Node: node, Recursive: false, Reason: "expand"}); err != nil {
		a.logf("自动同步失败：%v", err)
	}
}

func (a *App) onItemActivated() {
	node := a.selectedNode()
	if node == nil || !node.IsDir() || node.Loaded() {
		return
	}
	if entries, ok := a.cache.Children(node.Path()); ok {
		a.postUI(func() {
			a.model.ApplyFolder(node, entries)
			_ = a.tv.SetExpanded(node, true)
		})
		return
	}
	if a.client.Configured() {
		_ = a.syncer.Submit(&Job{Node: node, Recursive: false, Reason: "activate"})
		a.logf("双击「%s」：自动同步一层…", node.DisplayPath())
	}
}

func (a *App) onSelectionChanged() {
	node := a.selectedNode()
	if node == nil {
		return
	}
	if node.IsDir() {
		dirs, files := 0, 0
		for _, c := range node.children {
			if c.placeholder {
				continue
			}
			if c.IsDir() {
				dirs++
			} else {
				files++
			}
		}
		if node.Loaded() {
			state := "已加载"
			if syncedAt, ok := a.cache.SyncedAt(node.Path()); ok {
				state = formatTime(syncedAt)
			}
			a.setInfo(fmt.Sprintf("目录：%s　|　%d 个子目录 / %d 个文件　|　同步于 %s",
				node.DisplayPath(), dirs, files, state))
		} else {
			a.setInfo(fmt.Sprintf("目录：%s　|　子项未加载（展开或点击「同步选中目录」）", node.DisplayPath()))
		}
		return
	}

	e := node.Entry()
	etag := e.ETag
	if etag == "" {
		etag = "-"
	}
	a.setInfo(fmt.Sprintf("文件：%s　|　%s　|　修改于 %s　|　ETag %s",
		node.DisplayPath(), FormatSize(e.Size), formatTime(e.Modified), etag))
}

//
// UI 线程/状态辅助
//

// postUI 把函数投递到 UI 线程执行。
func (a *App) postUI(f func()) {
	if a.closed.Load() || a.mw == nil {
		return
	}
	a.mw.Synchronize(func() {
		if a.closed.Load() {
			return
		}
		f()
	})
}

func (a *App) setInfo(text string) {
	a.postUI(func() {
		if a.infoItem != nil {
			a.infoItem.SetText(text)
		}
	})
}

func (a *App) setStatus(text string) {
	a.postUI(func() {
		if a.statusItem != nil {
			a.statusItem.SetText(text)
		}
	})
}

// statusf 带格式化的状态栏更新。
func (a *App) statusf(format string, args ...any) {
	a.setStatus(fmt.Sprintf(format, args...))
}

func (a *App) setBusy(busy bool) {
	a.postUI(func() {
		if a.stopBtn != nil {
			a.stopBtn.SetEnabled(busy)
		}
		if a.syncBtn != nil {
			a.syncBtn.SetEnabled(!busy)
		}
		if a.syncAllBtn != nil {
			a.syncAllBtn.SetEnabled(!busy)
		}
		if a.settingsBtn != nil {
			a.settingsBtn.SetEnabled(!busy)
		}
		if !busy && a.statusItem != nil {
			a.statusItem.SetText("就绪")
		}
	})
}

func (a *App) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	log.Print(line)

	a.postUI(func() {
		if a.logEdit == nil {
			return
		}
		a.logEdit.AppendText(line + "\r\n")
		a.logLines++
		if a.logLines > 5000 {
			lines := strings.Split(a.logEdit.Text(), "\r\n")
			if len(lines) > 2500 {
				lines = lines[len(lines)-2500:]
			}
			a.logEdit.SetText(strings.Join(lines, "\r\n"))
			a.logLines = len(lines)
		}
		a.logEdit.ScrollToCaret()
	})
}
