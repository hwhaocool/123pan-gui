package ui

import (
	"fmt"
	"path"

	"github.com/lxn/walk"

	"pan123/internal/store"
)

// Node 是树形控件中的一个节点。节点指针在整个生命周期内保持稳定，
// 这样 walk 内部维护的 item->handle 映射不会失效；刷新目录时只替换
// children 切片并在原节点上发布 ItemsReset。
type Node struct {
	parent      *Node
	entry       store.Entry
	path        string // 相对 WebDAV 根目录的绝对路径，根为 "/"
	isRoot      bool
	loaded      bool
	placeholder bool
	children    []*Node
}

// NewRootNode 创建根节点（对应 WebDAV 根目录 "/"）。
func NewRootNode() *Node {
	n := &Node{isRoot: true, path: "/"}
	n.ensurePlaceholder()
	return n
}

// Text 实现 walk.TreeItem。
func (n *Node) Text() string {
	if n.isRoot {
		return "我的云盘"
	}
	if n.placeholder {
		return "（未同步：展开或点击「同步选中目录」）"
	}
	if n.entry.IsDir {
		return n.entry.Name
	}
	return fmt.Sprintf("%s   [%s]", n.entry.Name, FormatSize(n.entry.Size))
}

// Parent 实现 walk.TreeItem。
func (n *Node) Parent() walk.TreeItem {
	if n.parent == nil {
		return nil
	}
	return n.parent
}

// ChildCount 实现 walk.TreeItem。
func (n *Node) ChildCount() int { return len(n.children) }

// ChildAt 实现 walk.TreeItem。
func (n *Node) ChildAt(index int) walk.TreeItem { return n.children[index] }

// HasChild 实现 walk.HasChilder。未加载过的文件夹返回 true，
// 以便树控件显示展开箭头（展开时再从缓存/WebDAV 加载）。
func (n *Node) HasChild() bool {
	if !n.IsDir() {
		return false
	}
	if !n.loaded {
		return true
	}
	return len(n.children) > 0
}

// ensurePlaceholder 给「未加载的文件夹」补一个占位子项。
//
// 原因：ComCtl32 的 TreeView 只有在节点确实有子项时才允许展开，否则
// 点击展开箭头没有任何反应，walk 也就收不到 TVN_ITEMEXPANDED 事件，
// 懒加载 / 展开自动同步就永远不会被触发。占位子项会在同步完成后被真实
// 子项替换掉。
func (n *Node) ensurePlaceholder() {
	if n.loaded || !n.IsDir() || len(n.children) > 0 {
		return
	}
	n.children = []*Node{{
		parent:      n,
		path:        n.path,
		placeholder: true,
		entry:       store.Entry{Name: "（未同步）"},
	}}
}

// IsDir 表示节点是否文件夹。
func (n *Node) IsDir() bool { return n.isRoot || n.entry.IsDir }

// IsRoot 表示是否根节点。
func (n *Node) IsRoot() bool { return n.isRoot }

// Path 返回相对 WebDAV 根目录的绝对路径，例如 "/电影/2024"。
func (n *Node) Path() string { return n.path }

// DisplayPath 返回用于日志/状态栏的展示路径，例如 "我的云盘/电影/2024"。
func (n *Node) DisplayPath() string {
	if n.parent == nil {
		return "我的云盘"
	}
	return n.parent.DisplayPath() + "/" + n.entry.Name
}

// Entry 返回节点对应的文件信息。
func (n *Node) Entry() store.Entry { return n.entry }

// Loaded 表示该文件夹的子项是否已从缓存/WebDAV 加载过。
func (n *Node) Loaded() bool { return n.loaded }

// ChildDirs 返回直接子文件夹。
func (n *Node) ChildDirs() []*Node {
	dirs := make([]*Node, 0, len(n.children))
	for _, c := range n.children {
		if c.IsDir() {
			dirs = append(dirs, c)
		}
	}
	return dirs
}

// TreeModel 是基于 Node 的 walk.TreeModel 实现（懒加载）。
type TreeModel struct {
	walk.TreeModelBase

	root *Node
}

// NewTreeModel 创建树模型。
func NewTreeModel(root *Node) *TreeModel {
	return &TreeModel{root: root}
}

// LazyPopulation 返回 true：子节点只在该节点展开时才插入控件。
func (m *TreeModel) LazyPopulation() bool { return true }

// RootCount 实现 walk.TreeModel。
func (m *TreeModel) RootCount() int { return 1 }

// RootAt 实现 walk.TreeModel。
func (m *TreeModel) RootAt(index int) walk.TreeItem { return m.root }

// Root 返回根节点。
func (m *TreeModel) Root() *Node { return m.root }

// ApplyFolder 用最新的目录内容更新节点（按名称复用已有子节点指针），
// 然后通知树控件刷新该节点的子树。必须在 UI 线程调用。
func (m *TreeModel) ApplyFolder(node *Node, entries []store.Entry) {
	if node == nil || !node.IsDir() {
		return
	}

	old := make(map[string]*Node, len(node.children))
	for _, c := range node.children {
		if c.placeholder {
			continue
		}
		old[c.entry.Name] = c
	}

	children := make([]*Node, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		if c, ok := old[e.Name]; ok {
			delete(old, e.Name)
			if c.entry.IsDir != e.IsDir {
				// 类型发生变化（极端情况），丢弃旧的子树。
				c.children = nil
				c.loaded = false
			}
			c.entry = e
			c.path = joinPath(node.path, e.Name)
			children = append(children, c)
			continue
		}
		children = append(children, &Node{
			parent: node,
			entry:  e,
			path:   joinPath(node.path, e.Name),
		})
	}

	for _, c := range children {
		c.ensurePlaceholder()
	}

	node.children = children
	node.loaded = true

	m.PublishItemsReset(node)
}

func joinPath(parent, name string) string {
	if parent == "/" || parent == "" {
		return "/" + name
	}
	return path.Clean(parent + "/" + name)
}
