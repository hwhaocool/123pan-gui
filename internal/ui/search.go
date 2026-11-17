package ui

import (
	"github.com/lxn/walk"

	"pan123/internal/store"
)

// searchModel 是搜索结果表（TableView）的模型。
type searchModel struct {
	walk.TableModelBase

	hits []store.SearchHit
}

// RowCount 实现 walk.TableModel。
func (m *searchModel) RowCount() int { return len(m.hits) }

// Value 实现 walk.TableModel。
func (m *searchModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.hits) {
		return nil
	}
	h := m.hits[row]

	switch col {
	case 0:
		return h.Name
	case 1:
		if h.IsDir {
			return "文件夹"
		}
		return "文件"
	case 2:
		if h.IsDir {
			return "-"
		}
		return FormatSize(h.Size)
	case 3:
		return displayDir(h.Parent)
	}
	return nil
}

// SetHits 替换全部搜索结果。
func (m *searchModel) SetHits(hits []store.SearchHit) {
	m.hits = hits
	m.PublishRowsReset()
}

// Hit 返回第 row 行的结果。
func (m *searchModel) Hit(row int) (store.SearchHit, bool) {
	if row < 0 || row >= len(m.hits) {
		return store.SearchHit{}, false
	}
	return m.hits[row], true
}

// Hits 返回当前结果。
func (m *searchModel) Hits() []store.SearchHit { return m.hits }

// displayDir 把 WebDAV 路径转成展示路径，例如 "/电影/2024" -> "我的云盘/电影/2024"。
func displayDir(p string) string {
	p = store.NormalizePath(p)
	if p == "/" {
		return "我的云盘"
	}
	return "我的云盘" + p
}
