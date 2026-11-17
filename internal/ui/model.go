package ui

import (
	"path"
	"strings"

	"pan123/internal/store"
)

// Node 是目录树中的一个节点。节点指针在应用生命周期内保持稳定，
// 刷新目录时按名称复用子节点，从而保留展开/选中状态。
type Node struct {
	parent *Node
	entry  store.Entry
	path   string // 相对 WebDAV 根目录的绝对路径，根为 "/"
	root   bool

	loaded     bool
	loading    bool
	loadFailed bool

	children []*Node
}

// emptyChildren 用于「尚未加载的文件夹」：非 nil 的空切片让 Outline
// 显示展开箭头，展开后再由 App.reconcile 读取缓存/提交同步。
var emptyChildren = []*Node{}

// NewRootNode 创建根节点（对应 WebDAV 根目录 "/"）。
func NewRootNode() *Node {
	return &Node{root: true, path: "/"}
}

// Name 返回显示名称。
func (n *Node) Name() string {
	if n.root {
		return "我的云盘"
	}
	return n.entry.Name
}

// IsDir 表示节点是否文件夹。
func (n *Node) IsDir() bool { return n.root || n.entry.IsDir }

// IsRoot 表示是否根节点。
func (n *Node) IsRoot() bool { return n.root }

// Path 返回相对 WebDAV 根目录的绝对路径，例如 "/电影/2024"。
func (n *Node) Path() string { return n.path }

// Entry 返回节点的文件信息。
func (n *Node) Entry() store.Entry { return n.entry }

// Loaded 表示子项是否已从缓存/WebDAV 加载过。
func (n *Node) Loaded() bool { return n.loaded }

// Children 返回直接子节点。
func (n *Node) Children() []*Node { return n.children }

// DisplayPath 返回用于日志/状态栏的展示路径，例如 "我的云盘/电影/2024"。
func (n *Node) DisplayPath() string {
	if n.parent == nil {
		return n.Name()
	}
	return n.parent.DisplayPath() + "/" + n.entry.Name
}

// outlineChildren 供 ui.Outline 使用：
//   - 文件返回 nil（叶子，无展开箭头）
//   - 未加载的文件夹返回非 nil 空切片（显示箭头，展开后触发加载）
//   - 已加载的空文件夹返回 nil（不再显示箭头）
func (n *Node) outlineChildren() []*Node {
	if !n.IsDir() {
		return nil
	}
	if !n.loaded {
		return emptyChildren
	}
	if len(n.children) == 0 {
		return nil
	}
	return n.children
}

// mergeChildren 用最新列表更新子节点，按名称复用已有指针。
func (n *Node) mergeChildren(entries []store.Entry) {
	old := make(map[string]*Node, len(n.children))
	for _, c := range n.children {
		old[c.entry.Name] = c
	}

	next := make([]*Node, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		if c, ok := old[e.Name]; ok {
			delete(old, e.Name)
			if c.entry.IsDir != e.IsDir {
				// 类型变化：丢弃旧的子树
				c.children = nil
				c.loaded = false
			}
			c.entry = e
			next = append(next, c)
			continue
		}
		next = append(next, &Node{
			parent: n,
			entry:  e,
			path:   joinPath(n.path, e.Name),
		})
	}
	n.children = next
}

// find 从当前节点开始按路径查找子节点。
func (n *Node) find(p string) *Node {
	p = store.NormalizePath(p)
	if p == n.path {
		return n
	}
	if n.path != "/" && !strings.HasPrefix(p, n.path+"/") {
		return nil
	}
	rel := strings.TrimPrefix(p, n.path)
	for _, seg := range strings.Split(strings.TrimPrefix(rel, "/"), "/") {
		if seg == "" {
			continue
		}
		var next *Node
		for _, c := range n.children {
			if c.entry.Name == seg {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

func joinPath(parent, name string) string {
	if parent == "/" || parent == "" {
		return "/" + name
	}
	return path.Clean(parent + "/" + name)
}
