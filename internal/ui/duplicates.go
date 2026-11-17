package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// dupGroupLimit 是单次检测最多展示的同名文件组数。
const dupGroupLimit = 500

// detectDuplicates 扫描本地 SQLite 缓存，找出同名文件（不统计文件夹），
// 并切换到重复文件视图；整个过程不访问网络。
func (a *App) detectDuplicates() {
	if a.cache == nil {
		a.addLog("本地数据库不可用，无法检测重复文件")
		a.status = "检测失败"
		return
	}

	groups, err := a.cache.DuplicateFiles(2, dupGroupLimit)
	if err != nil {
		a.addLog("重复文件检测失败：%v", err)
		a.status = "检测失败"
		return
	}

	a.dupGroups = groups
	a.dupList = ui.ListState{}
	a.dupFileList = ui.ListState{}
	a.dupSelected = -1
	a.dupFileSelected = -1
	a.dupShownGroup = -1
	if len(groups) > 0 {
		a.dupSelected = 0
	}

	a.showDupes = true
	a.showResults = false

	if len(groups) == 0 {
		a.addLog("重复文件检测：本地缓存中没有同名文件")
		a.status = "没有同名文件"
		return
	}

	total := 0
	for _, g := range groups {
		total += len(g.Files)
	}
	a.status = "重复文件检测完成"
	a.addLog("重复文件检测：%d 组同名文件（共 %d 个文件，仅本地缓存）", len(groups), total)
	if len(groups) >= dupGroupLimit {
		a.addLog("同名文件组较多，只显示前 %d 组；可先同步更多目录后再检测", dupGroupLimit)
	}
}

// duplicatesView 显示检测结果：左侧为同名文件名列表，右侧为这些文件的位置与时间信息。
func (a *App) duplicatesView(c *ui.Context, t *ui.Theme) {
	total := 0
	for _, g := range a.dupGroups {
		total += len(g.Files)
	}

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(10, 6).Gap(8).AlignItems(ui.Center).Children(func() {
			caption := "重复文件检测：本地缓存中没有同名文件"
			if len(a.dupGroups) > 0 {
				caption = fmt.Sprintf("同名文件：%d 组 / %d 个文件（仅本地缓存，不含文件夹）",
					len(a.dupGroups), total)
			}
			ui.Text(c, caption).TextColor(t.TextMuted).Grow(1).SingleLine()
			if ui.Button(c, "重新检测").Clicked() {
				a.detectDuplicates()
			}
			if ui.Button(c, "返回目录树").Clicked() {
				a.showDupes = false
			}
		})

		if len(a.dupGroups) == 0 {
			ui.Column(c).Fill().Padding(24).Gap(6).Children(func() {
				ui.Text(c, "没有发现同名文件").FontSize(15).Bold()
				ui.Text(c, "检测只使用本地 SQLite 缓存；先同步更多目录，再点「重新检测」。").TextColor(t.TextMuted)
			})
			return
		}

		ui.Split(c, &a.dupWidth, func() {
			a.dupNamesView(c, t)
		}, func() {
			a.dupFilesView(c, t)
		}).Fill()
	})
}

// dupNamesView 是左侧同名文件名列表，行尾显示该名字出现了几次。
func (a *App) dupNamesView(c *ui.Context, t *ui.Theme) {
	a.dupList.Selected = &a.dupSelected
	a.dupList.Key = func(i int) any { return strings.ToLower(a.dupGroups[i].Name) }
	ui.List(c, &a.dupList, len(a.dupGroups), func(i int) {
		g := a.dupGroups[i]
		ui.Row(c).Fill().Padding(4, 8).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconFile).Size(16, 16).TextColor(t.TextMuted)
			ui.Text(c, g.Name).SingleLine().Grow(1)
			ui.Text(c, strconv.Itoa(len(g.Files))+" 个").FontSize(11).TextColor(t.TextMuted)
		})
	}).Grow(1).Label("同名文件列表")
}

// dupFilesView 是右侧文件列表：名称 / 路径 / 大小 / 创建时间 / 修改时间。
func (a *App) dupFilesView(c *ui.Context, t *ui.Theme) {
	if a.dupSelected < 0 || a.dupSelected >= len(a.dupGroups) {
		ui.Column(c).Fill().Padding(24).Children(func() {
			ui.Text(c, "请选择左侧的文件名").TextColor(t.TextMuted)
		})
		return
	}

	g := a.dupGroups[a.dupSelected]
	if a.dupShownGroup != a.dupSelected {
		a.dupShownGroup = a.dupSelected
		a.dupFileSelected = -1
	}
	files := g.Files

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Padding(10, 8).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconFile).Size(16, 16).TextColor(t.TextMuted)
			ui.Text(c, g.Name).Bold().SingleLine()
			ui.Text(c, fmt.Sprintf("%d 个同名文件", len(files))).TextColor(t.TextMuted)
		})

		a.dupFileList.Selected = &a.dupFileSelected
		a.dupFileList.Key = func(i int) any { return files[i].Path }
		cols := []ui.TableColumn{
			{Title: "名称"},
			{Title: "路径"},
			{Title: "大小", Width: 100, Align: ui.End},
			{Title: "创建时间", Width: 165},
			{Title: "修改时间", Width: 165},
		}
		if ui.Table(c, &a.dupFileList, cols, len(files), func(row, col int) {
			h := files[row]
			switch col {
			case 0:
				ui.Text(c, h.Name).SingleLine()
			case 1:
				ui.Text(c, displayDir(h.Parent)).TextColor(t.TextMuted).SingleLine()
			case 2:
				ui.Text(c, FormatSize(h.Size))
			case 3:
				ui.Text(c, formatTime(h.Created)).TextColor(t.TextMuted)
			case 4:
				ui.Text(c, formatTime(h.Modified)).TextColor(t.TextMuted)
			}
		}).Grow(1).Label("同名文件详情").Submitted() {
			if a.dupFileSelected >= 0 && a.dupFileSelected < len(files) {
				a.reveal(files[a.dupFileSelected])
			}
		}
	})
}
