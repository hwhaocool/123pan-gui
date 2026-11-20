// Package ui 是 123 云盘客户端基于 mygo 原生 UI 的界面层。
package ui

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"pan123/internal/config"
	"pan123/internal/store"
	"pan123/internal/webdav"
)

// App 保存应用状态，并实现 mygo 的视图函数。
type App struct {
	cfg     *config.Config
	cfgPath string

	cache  *store.Cache
	client *webdav.Client
	syncer *Syncer
	root   *Node

	win    *mygo.Window
	closed atomic.Bool

	outline       ui.OutlineState[*Node]
	selectedRow   int
	selected      *Node
	pendingReveal *Node

	busy   bool
	status string
	info   string

	logs       []string
	logList    ui.ListState
	topPercent float32 // 上方面板（目录树 + 文件列表）占高度百分比

	leftWidth     float32
	fileList      ui.ListState
	fileSelected  int
	selectedEntry *Node
	fileShownDir  string
	pendingFile   string
	lastSelected  *Node
	crumbIndex    int

	searchQuery    string
	searchResults  []store.SearchHit
	searchList     ui.ListState
	searchSelected int
	showResults    bool

	showDupes       bool
	dupGroups       []store.DuplicateGroup
	dupList         ui.ListState
	dupSelected     int
	dupFileList     ui.ListState
	dupFileSelected int
	dupShownGroup   int
	dupWidth        float32

	settingsOpen    bool
	draft           settingsDraft
	settingsStatus  string
	settingsTesting bool

	deleting     bool
	deleteTarget *Node
	deleteOpen   bool

	initErr error
}

// NewApp 读取配置、打开本地数据库并准备好 WebDAV 客户端。
// 初始化失败时返回带 initErr 的 App，界面会显示错误信息。
func NewApp() *App {
	a := &App{
		status:          "就绪",
		topPercent:      74,
		leftWidth:       320,
		fileSelected:    -1,
		selectedRow:     -1,
		searchSelected:  -1,
		dupSelected:     -1,
		dupFileSelected: -1,
		dupShownGroup:   -1,
		dupWidth:        260,
	}

	cfg, path, err := config.Load()
	if err != nil {
		log.Printf("加载配置: %v", err)
	}
	a.cfg, a.cfgPath = cfg, path

	cachePath, err := config.CachePath()
	if err != nil {
		a.initErr = fmt.Errorf("无法确定数据库路径: %w", err)
		return a
	}
	a.cache, err = store.Open(cachePath)
	if err != nil {
		a.initErr = fmt.Errorf("打开本地数据库失败: %w", err)
		return a
	}

	a.root = NewRootNode()
	a.client = webdav.New(a.clientOptions())
	a.client.OnWait = func(d time.Duration) {
		if d >= 2*time.Second {
			a.logf("触发限速，等待 %s …", d.Round(100*time.Millisecond))
		}
	}

	a.syncer = NewSyncer(a.client, a.cache)
	a.syncer.synchronize = func(f func()) { mygo.RunOnMain(f) }
	a.syncer.apply = func(n *Node, entries []store.Entry) { a.applyEntries(n, entries) }
	a.syncer.logf = a.logf
	a.syncer.status = func(s string) { a.async(func() { a.status = s }) }
	a.syncer.busy = func(b bool) { a.async(func() { a.busy = b }) }
	a.syncer.done = func(n *Node) {
		a.async(func() {
			n.loading = false
			if n.loaded {
				n.loadFailed = false
				a.outline.Open.Add(n)
			} else {
				n.loadFailed = true
			}
			if t := a.cache.LastSyncedAt(); !t.IsZero() {
				a.cfg.LastSyncAt = t
				if err := a.cfg.Save(a.cfgPath); err != nil {
					a.addLog("保存配置失败：%v", err)
				}
			}
		})
	}

	return a
}

// Attach 在窗口创建后调用：启动同步 worker 并开始首次加载。
func (a *App) Attach(win *mygo.Window) {
	a.win = win
	if a.initErr != nil {
		return
	}
	a.syncer.Start()
	go a.bootstrap()
}

// Shutdown 在应用退出后调用：停止同步、合并并关闭数据库、保存配置。
func (a *App) Shutdown() {
	a.closed.Store(true)
	if a.syncer != nil {
		a.syncer.Close()
	}
	if a.cache != nil {
		last := a.cache.LastSyncedAt()
		if a.cfg != nil && !last.IsZero() {
			a.cfg.LastSyncAt = last
		}
		_ = a.cache.Flush()
		_ = a.cache.Close()
	}
	if a.cfg != nil && a.cfgPath != "" {
		_ = a.cfg.Save(a.cfgPath)
	}
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

//
// 视图
//

// View 构建窗口界面（mygo 每帧调用）。
func (a *App) View(c *ui.Context) {
	t := c.Theme()

	if a.initErr != nil {
		a.errorView(c, t)
		return
	}
	if a.settingsOpen {
		a.settingsView(c, t)
		return
	}

	// 先读取展开目录的本地缓存 / 提交同步，再构建树。
	a.reconcile()

	ui.Column(c).Fill().Gap(0).Children(func() {
		a.toolbar(c)
		ui.Divider(c)
		a.body(c, t)
		ui.Divider(c)
		a.statusBar(c, t)
	})

	a.alert(c)
}

func (a *App) errorView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Fill().Padding(24).Gap(10).Children(func() {
		ui.Text(c, "启动失败").FontSize(20).Bold()
		ui.Text(c, a.initErr.Error()).TextColor(t.Danger)
	})
}

func (a *App) toolbar(c *ui.Context) {
	ui.Row(c).Padding(10, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.PrimaryButton(c, "同步选中目录(和子目录)").
			Disabled(a.busy).
			OnClick(func() { a.syncSelected(true) })
		ui.Button(c, "同步选中目录(单层)").
			Disabled(a.busy).
			OnClick(func() { a.syncSelected(false) })
		ui.Button(c, "停止").Disabled(!a.busy).OnClick(func() {
			a.syncer.Stop()
			a.status = "正在停止…"
		})
		ui.Button(c, "重复文件检测").
			Tooltip("在本地缓存中查找同名文件（不含文件夹）").
			OnClick(a.detectDuplicates)

		ui.Spacer(c)

		if ui.SearchField(c, &a.searchQuery).
			Placeholder("搜索文件名 / 文件夹（仅本地缓存）").
			Width(300).
			Changed() {
			a.runSearch(false)
		}
		if a.showResults || a.showDupes {
			if ui.Button(c, "返回目录树").Clicked() {
				a.showResults = false
				a.showDupes = false
			}
		} else if strings.TrimSpace(a.searchQuery) != "" {
			if ui.Button(c, "查看搜索结果").Clicked() {
				a.showResults = true
			}
		}
		ui.Button(c, "设置…").Disabled(a.busy).OnClick(a.openSettings)
	})
}

func (a *App) body(c *ui.Context, t *ui.Theme) {
	ui.SplitVerticalPercent(c, &a.topPercent, func() {
		if a.showDupes {
			a.duplicatesView(c, t)
			return
		}
		if a.showResults {
			a.resultsView(c, t)
			return
		}
		ui.Split(c, &a.leftWidth, func() {
			a.treeView(c, t)
		}, func() {
			a.filesView(c, t)
		}).Fill()
	}, func() {
		a.logView(c, t)
	}).SplitReset(74).Grow(1)
}

func (a *App) treeView(c *ui.Context, t *ui.Theme) {
	if a.root == nil {
		return
	}

	a.outline.List.Selected = &a.selectedRow
	ui.Outline(c, &a.outline, []*Node{a.root}, func(n *Node) []*Node {
		return n.outlineChildren()
	}, func(n *Node) {
		ui.Icon(c, iconFolder).Size(16, 16).TextColor(t.Accent)
		ui.Text(c, n.Name()).SingleLine()
		if n.loading {
			ui.Spinner(c)
		}
	}).Grow(1).Label("云盘目录")

	// Outline 在构建中写入选中行，构建后读取选中的节点。
	if a.selectedRow >= 0 && a.selectedRow < a.outline.Rows() {
		a.selected = a.outline.Item(a.selectedRow)
	} else {
		a.selected = nil
	}

	// 选中文件夹时立即加载（缓存优先），右侧文件列表随后就能显示内容。
	if a.selected != a.lastSelected {
		a.lastSelected = a.selected
		a.fileList = ui.ListState{}
		a.fileSelected = -1
		a.selectedEntry = nil
		if a.selected != nil && a.selected.IsDir() &&
			!a.selected.loaded && !a.selected.loading && !a.selected.loadFailed {
			a.loadNode(a.selected)
		}
	}

	a.scrollToPendingReveal(c)
}

func (a *App) filesView(c *ui.Context, t *ui.Theme) {
	dir := a.selected
	if dir == nil {
		dir = a.root
	}
	if dir == nil || !dir.IsDir() {
		return
	}

	if a.fileShownDir != dir.Path() {
		a.fileShownDir = dir.Path()
		a.fileSelected = -1
		a.selectedEntry = nil
	}

	entries := dir.Children()

	revealIndex := -1
	if a.pendingFile != "" {
		for i, e := range entries {
			if e.Path() == a.pendingFile {
				revealIndex = i
				a.fileSelected = i
				break
			}
		}
		if revealIndex < 0 {
			a.pendingFile = ""
		}
	}

	names, nodes := a.breadcrumb(dir)

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(10, 8).Gap(8).AlignItems(ui.Center).Children(func() {
			a.crumbIndex = len(names) - 1
			if ui.Breadcrumbs(c, names, &a.crumbIndex).Label("路径").Grow(1).Changed() {
				if a.crumbIndex >= 0 && a.crumbIndex < len(nodes) {
					a.navigateTo(nodes[a.crumbIndex])
				}
			}
			if dir.loading {
				ui.Spinner(c)
			}
			ui.Text(c, fmt.Sprintf("%d 个文件夹 / %d 个文件", len(dir.dirs), len(dir.files))).
				TextColor(t.TextMuted).SingleLine()
		})

		a.fileList.Selected = &a.fileSelected
		a.fileList.Key = func(i int) any { return entries[i].Path() }
		cols := []ui.TableColumn{
			{Title: "名称"},
			{Title: "大小", Width: 100, Align: ui.End},
			{Title: "创建时间", Width: 165},
			{Title: "修改时间", Width: 165},
			{Title: "", Width: 44, Align: ui.Center},
		}
		if ui.Table(c, &a.fileList, cols, len(entries), func(row, col int) {
			e := entries[row].Entry()
			switch col {
			case 0:
				if e.IsDir {
					ui.Icon(c, iconFolder).Size(16, 16).TextColor(t.Accent)
				} else {
					ui.Icon(c, iconFile).Size(16, 16).TextColor(t.TextMuted)
				}
				ui.Text(c, e.Name).SingleLine()
			case 1:
				if e.IsDir {
					ui.Text(c, "-").TextColor(t.TextMuted)
				} else {
					ui.Text(c, FormatSize(e.Size))
				}
			case 2:
				ui.Text(c, formatTime(e.Created)).TextColor(t.TextMuted)
			case 3:
				ui.Text(c, formatTime(e.Modified)).TextColor(t.TextMuted)
			case 4:
				node := entries[row]
				ui.Button(c, "").
					Label("删除").
					Tooltip("删除「" + node.Name() + "」").
					Disabled(a.busy || a.deleting).
					OnClick(func() { a.askDelete(node) }).
					Children(func() {
						ui.Icon(c, iconTrash).Size(16, 16).TextColor(t.Danger)
					})
			}
		}).Grow(1).Label("文件列表").Submitted() {
			if a.fileSelected >= 0 && a.fileSelected < len(entries) && entries[a.fileSelected].IsDir() {
				a.navigateTo(entries[a.fileSelected])
			}
		}
	})

	if a.fileSelected >= 0 && a.fileSelected < len(entries) {
		a.selectedEntry = entries[a.fileSelected]
	} else {
		a.selectedEntry = nil
	}

	if revealIndex >= 0 {
		a.fileList.ScrollTo(revealIndex, ui.Center)
		a.pendingFile = ""
		c.Invalidate()
	}
}

// breadcrumb 返回从根到 dir 的路径名与对应节点。
func (a *App) breadcrumb(dir *Node) (names []string, nodes []*Node) {
	for n := dir; n != nil; n = n.parent {
		names = append(names, n.Name())
		nodes = append(nodes, n)
	}
	for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
	return names, nodes
}

// navigateTo 跳转到某个目录：展开祖先、选中该节点，并刷新右侧列表。
func (a *App) navigateTo(n *Node) {
	if n == nil || !n.IsDir() {
		return
	}
	for p := n; p != nil; p = p.parent {
		a.outline.Open.Add(p)
	}
	a.showResults = false
	a.showDupes = false
	a.pendingReveal = n
	a.pendingFile = ""
	a.fileShownDir = ""
	a.fileSelected = -1
	a.selectedEntry = nil
	a.selected = n
	a.lastSelected = n
	if !n.loaded && !n.loading && !n.loadFailed {
		a.loadNode(n)
	}
}

func (a *App) resultsView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(10, 6).Gap(8).AlignItems(ui.Center).Children(func() {
			caption := fmt.Sprintf("搜索「%s」：%d 条结果（仅本地缓存）", strings.TrimSpace(a.searchQuery), len(a.searchResults))
			ui.Text(c, caption).TextColor(t.TextMuted).Grow(1).SingleLine()
			if ui.Button(c, "返回目录树").Clicked() {
				a.showResults = false
			}
		})

		a.searchList.Selected = &a.searchSelected
		a.searchList.Key = func(i int) any { return a.searchResults[i].Path }
		cols := []ui.TableColumn{
			{Title: "名称"},
			{Title: "类型", Width: 80},
			{Title: "大小", Width: 110, Align: ui.End},
			{Title: "位置", Width: 340},
		}
		if ui.Table(c, &a.searchList, cols, len(a.searchResults), func(row, col int) {
			h := a.searchResults[row]
			switch col {
			case 0:
				ui.Text(c, h.Name).SingleLine()
			case 1:
				if h.IsDir {
					ui.Text(c, "文件夹")
				} else {
					ui.Text(c, "文件")
				}
			case 2:
				if !h.IsDir {
					ui.Text(c, FormatSize(h.Size))
				}
			case 3:
				ui.Text(c, displayDir(h.Parent)).TextColor(t.TextMuted).SingleLine()
			}
		}).Grow(1).Label("搜索结果").Submitted() {
			if a.searchSelected >= 0 && a.searchSelected < len(a.searchResults) {
				a.reveal(a.searchResults[a.searchSelected])
			}
		}
	})
}

func (a *App) logView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Fill().Background(t.Surface).Children(func() {
		ui.Row(c).Padding(10, 4).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "日志").Bold()
			ui.Spacer(c)
			if ui.Button(c, "清空").Clicked() {
				a.logs = nil
			}
		})
		a.logList.FollowEnd = true
		ui.List(c, &a.logList, len(a.logs), func(i int) {
			ui.Text(c, a.logs[i]).SingleLine().Padding(1, 10)
		}).Grow(1)
	})
}

func (a *App) statusBar(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Padding(10, 6).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.infoText()).TextColor(t.TextMuted).Grow(1).SingleLine()
		if a.busy {
			ui.Spinner(c)
		}
		ui.Text(c, a.status).SingleLine()
	})
}

func (a *App) alert(c *ui.Context) {
	if a.deleteTarget != nil {
		title := "删除「" + a.deleteTarget.Name() + "」？"
		message := "会从 WebDAV 服务器删除该文件，无法撤销。"
		if a.deleteTarget.IsDir() {
			message = "会递归删除该文件夹及其全部内容，无法撤销。"
		}
		if ret := ui.AlertDialog(c, &a.deleteOpen, title, message, "取消", "删除"); ret >= 0 {
			target := a.deleteTarget
			a.deleteTarget = nil
			if ret == 1 {
				a.deleteNode(target)
			}
		}
	}
}

//
// 交互
//

func (a *App) infoText() string {
	if sel := a.selectedEntry; sel != nil {
		return describeNode(sel)
	}
	if n := a.selected; n != nil {
		return describeNode(n)
	}
	if a.info != "" {
		return a.info
	}
	return "就绪"
}

func describeNode(n *Node) string {
	if n.IsDir() {
		if n.Loaded() {
			return fmt.Sprintf("目录：%s　|　%d 个子目录 / %d 个文件　|　已加载",
				n.DisplayPath(), len(n.dirs), len(n.files))
		}
		return fmt.Sprintf("目录：%s　|　子项未加载（双击进入或展开）", n.DisplayPath())
	}

	e := n.Entry()
	etag := e.ETag
	if etag == "" {
		etag = "-"
	}
	return fmt.Sprintf("文件：%s　|　%s　|　创建于 %s　|　修改于 %s　|　ETag %s",
		n.DisplayPath(), FormatSize(e.Size), formatTime(e.Created), formatTime(e.Modified), etag)
}

// syncSelected 同步目录树中选中的文件夹（未选中时用根目录）：
// recursive 为 true 时递归子目录，否则只刷新这一层。
func (a *App) syncSelected(recursive bool) {
	n := a.selected
	if n == nil {
		n = a.root
	}
	if n == nil || !n.IsDir() {
		a.addLog("请先在目录树中选择一个文件夹")
		a.status = "请选择文件夹"
		return
	}
	a.submitSync(n, recursive)
}

func (a *App) submitSync(n *Node, recursive bool) {
	if a.deleting {
		a.addLog("正在删除，请稍后再同步")
		a.status = "正在删除"
		return
	}
	if !a.client.Configured() {
		a.addLog("尚未配置 WebDAV 地址，请打开「设置…」")
		a.status = "未配置 WebDAV"
		return
	}
	n.loadFailed = false
	if err := a.syncer.Submit(&Job{Node: n, Recursive: recursive, Reason: "manual"}); err != nil {
		a.addLog("未能加入同步队列：%v", err)
		return
	}
	a.addLog("已加入同步队列：%s（%s）", n.DisplayPath(), onOff(recursive, "含子目录", "仅本层"))
}

// askDelete 弹出删除确认。
func (a *App) askDelete(n *Node) {
	if n == nil || n.parent == nil {
		return
	}
	if a.busy {
		a.addLog("正在同步，暂时不能删除")
		a.status = "请等待同步结束"
		return
	}
	if a.deleting {
		return
	}
	a.deleteTarget = n
	a.deleteOpen = true
}

// deleteNode 调用 WebDAV DELETE 删除条目，然后清理本地缓存与界面。
func (a *App) deleteNode(n *Node) {
	if n == nil || n.parent == nil || a.deleting {
		return
	}

	path := n.Path()
	isDir := n.IsDir()
	display := n.DisplayPath()

	a.deleting = true
	a.status = "正在删除：" + display

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		err := a.client.Delete(ctx, path, isDir)
		if err != nil && !webdav.IsNotFound(err) {
			a.async(func() {
				a.deleting = false
				a.addLog("删除失败：%s：%v", display, err)
				a.status = "删除失败"
			})
			return
		}
		notFound := err != nil

		cacheErr := a.cache.Delete(path)

		a.async(func() {
			a.deleting = false
			if cacheErr != nil {
				a.addLog("清理本地缓存失败：%v", cacheErr)
			}
			a.removeNode(n)
			if notFound {
				a.addLog("服务器上已不存在：%s（已从本地移除）", display)
			} else {
				a.addLog("已删除：%s", display)
			}
			a.status = "就绪"
		})
	}()
}

// removeNode 从树模型和文件列表中移除节点（主线程）。
func (a *App) removeNode(n *Node) {
	parent := n.parent
	if parent == nil {
		return
	}
	parent.removeChild(n)
	a.outline.Open.Remove(n)

	if a.selectedEntry == n {
		a.selectedEntry = nil
		a.fileSelected = -1
	}

	// 删掉的正是当前选中的目录：回到父目录。
	if a.selected == n || a.lastSelected == n {
		a.selected = parent
		a.lastSelected = parent
		a.pendingReveal = parent
		a.fileShownDir = ""
		a.fileSelected = -1
		a.selectedEntry = nil
	}

	// 列表里的行号会变，清掉选择避免指向别的条目。
	if a.fileSelected >= 0 && a.selected == parent {
		a.fileSelected = -1
	}
}

// reconcile 在构建界面前调用：展开但尚未加载的目录，先读本地缓存，
// 没有缓存再提交一次「仅本层」的同步。
func (a *App) reconcile() {
	if a.root == nil {
		return
	}
	for n := range a.outline.Open.All() {
		if n.IsDir() && !n.loaded && !n.loading && !n.loadFailed {
			a.loadNode(n)
		}
	}
	if n := a.selected; n != nil && n.IsDir() && !n.loaded && !n.loading && !n.loadFailed {
		a.loadNode(n)
	}
}

func (a *App) loadNode(n *Node) {
	if entries, ok := a.cache.Children(n.Path()); ok {
		a.applyEntries(n, entries)
		return
	}
	if !a.client.Configured() {
		n.loadFailed = true
		a.addLog("目录 %s 没有本地缓存，且未配置 WebDAV", n.DisplayPath())
		return
	}
	n.loading = true
	if err := a.syncer.Submit(&Job{Node: n, Recursive: false, Reason: "expand"}); err != nil {
		n.loading = false
		n.loadFailed = true
		a.addLog("自动同步失败：%v", err)
	} else {
		a.addLog("展开「%s」：没有缓存，自动同步一层…", n.DisplayPath())
	}
}

func (a *App) applyEntries(n *Node, entries []store.Entry) {
	if n == nil || !n.IsDir() {
		return
	}
	n.mergeChildren(entries)
	n.loaded = true
	n.loading = false
	n.loadFailed = false
}

func (a *App) runSearch(announce bool) {
	q := strings.TrimSpace(a.searchQuery)
	if q == "" {
		a.searchResults = nil
		a.showResults = false
		a.searchSelected = -1
		return
	}

	hits, err := a.cache.Search(q, 500)
	if err != nil {
		a.addLog("本地搜索失败：%v", err)
		a.status = "搜索失败"
		return
	}
	a.searchResults = hits
	a.searchSelected = -1
	a.showResults = true
	a.showDupes = false
	if announce {
		if len(hits) == 0 {
			a.addLog("搜索「%s」：本地缓存中没有匹配项", q)
		} else {
			a.addLog("搜索「%s」：%d 条结果（仅本地缓存）", q, len(hits))
		}
	}
}

// reveal 在目录树中定位一条搜索结果：只从本地缓存展开祖先目录，不访问网络。
func (a *App) reveal(hit store.SearchHit) {
	p := store.NormalizePath(hit.Path)
	if entries, ok := a.cache.Children("/"); ok {
		a.applyEntries(a.root, entries)
	}
	a.outline.Open.Add(a.root)

	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	node := a.root
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		var next *Node
		for _, c := range node.Children() {
			if c.Entry().Name == seg {
				next = c
				break
			}
		}
		if next == nil {
			a.addLog("无法定位：%s 的上级目录尚未缓存", p)
			a.status = "无法定位"
			return
		}
		node = next
		if node.IsDir() && i < len(segs)-1 {
			if entries, ok := a.cache.Children(node.Path()); ok {
				a.applyEntries(node, entries)
			} else {
				a.addLog("无法定位：%s 尚未缓存", node.DisplayPath())
				a.status = "无法定位"
				return
			}
			a.outline.Open.Add(node)
		}
	}
	if node.IsDir() {
		a.outline.Open.Add(node)
	}

	target := node
	a.pendingFile = ""
	if !node.IsDir() {
		// 文件在右侧列表里定位，树里选中它的父目录。
		target = node.parent
		a.pendingFile = node.Path()
	}

	a.showResults = false
	a.showDupes = false
	a.pendingReveal = target
	a.status = "已定位：" + node.DisplayPath()
}

// scrollToPendingReveal 在 Outline 构建后找到目标行并滚动过去。
func (a *App) scrollToPendingReveal(c *ui.Context) {
	if a.pendingReveal == nil {
		return
	}
	for i := 0; i < a.outline.Rows(); i++ {
		if a.outline.Item(i) == a.pendingReveal {
			a.selectedRow = i
			a.selected = a.pendingReveal
			a.lastSelected = a.pendingReveal
			a.outline.List.ScrollTo(i, ui.Center)
			a.pendingReveal = nil
			c.Invalidate()
			return
		}
	}
}

//
// 后台任务与状态更新
//

func (a *App) bootstrap() {
	if a.initErr != nil {
		return
	}

	a.async(func() {
		a.addLog("123 云盘客户端已启动（mygo 原生 UI）")
		a.addLog("配置文件：%s", a.cfgPath)
		a.addLog("数据库：%s", a.cache.Path())
		a.addLog("限速：QPS=%.1f（两次请求最小间隔 %s）", a.client.QPS(), a.client.Interval())

		if folders, items := a.cache.Stats(); folders > 0 {
			a.addLog("本地缓存：%d 个已同步目录 / %d 项，最近同步于 %s", folders, items, formatTime(a.cache.LastSyncedAt()))
		}
		if entries, ok := a.cache.Children("/"); ok {
			a.applyEntries(a.root, entries)
			a.outline.Open.Add(a.root)
			a.selectedRow = 0
			a.selected = a.root
			a.addLog("已从本地数据库加载根目录（%d 项）", len(entries))
		} else {
			a.addLog("本地还没有根目录缓存")
		}
	})

	if !a.client.Configured() {
		a.async(func() {
			a.addLog("尚未配置 WebDAV 地址，请点击「设置…」填写后再同步")
			a.info = "未配置 WebDAV 地址"
			a.status = "等待配置"
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	err := a.client.Check(ctx)
	cancel()
	if err != nil {
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

	if _, ok := a.cache.Children("/"); !ok {
		a.async(func() {
			a.addLog("将自动同步根目录（仅第一层）…")
			a.root.loading = true
		})
		if err := a.syncer.Submit(&Job{Node: a.root, Recursive: false, Reason: "startup"}); err != nil {
			a.async(func() {
				a.root.loading = false
				a.addLog("提交同步任务失败：%v", err)
			})
		}
	}
}

// async 把修改状态的函数投递到主线程并重绘；窗口销毁后丢弃。
func (a *App) async(fn func()) {
	if a.win == nil || a.closed.Load() {
		return
	}
	a.win.Update(fn)
}

// logf 可在任意 goroutine 调用。
func (a *App) logf(format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	log.Print(msg)
	a.async(func() { a.addLog("%s", msg) })
}

// addLog 只能在主线程调用（视图、动作或 async 内）。
func (a *App) addLog(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	a.logs = append(a.logs, line)
	if len(a.logs) > 3000 {
		a.logs = a.logs[len(a.logs)-3000:]
	}
}

// 供 settings.go 使用
func (a *App) parseQPS(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}
