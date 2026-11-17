package ui

import (
	"fmt"

	"github.com/lxn/walk"

	"pan123/internal/api"
)

// Node 是树形控件中的一个节点。节点指针在整个生命周期内保持稳定，
// 这样 walk 内部维护的 item->handle 映射不会失效；刷新目录时只替换
// children 切片并在原节点上发布 ItemsReset。
type Node struct {
	parent      *Node
	file        api.FileInfo
	isRoot      bool
	loaded      bool
	placeholder bool
	children    []*Node
}

// placeholderFileID 是占位子项的伪文件 ID。
const placeholderFileID int64 = -1

// NewRootNode 创建根节点（对应 FileID 0 的「我的云盘」）。
func NewRootNode() *Node {
	n := &Node{isRoot: true}
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
	if n.file.IsDir() {
		return n.file.Filename
	}
	txt := fmt.Sprintf("%s   [%s]", n.file.Filename, FormatSize(n.file.Size))
	if n.file.Status > 100 {
		txt += "  [审核未通过]"
	}
	return txt
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
// 以便树控件显示展开箭头（展开时再从缓存/接口加载）。
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
		placeholder: true,
		file:        api.FileInfo{FileID: placeholderFileID, Filename: "（未同步）", Type: 0},
	}}
}

// IsDir 表示节点是否文件夹。
func (n *Node) IsDir() bool { return n.isRoot || n.file.IsDir() }

// FileID 返回节点对应的云端文件 ID，根目录为 0。
func (n *Node) FileID() int64 { return n.file.FileID }

// File 返回节点对应的文件信息。
func (n *Node) File() api.FileInfo { return n.file }

// IsRoot 表示是否根节点。
func (n *Node) IsRoot() bool { return n.isRoot }

// Loaded 表示该文件夹的子项是否已从缓存/接口加载过。
func (n *Node) Loaded() bool { return n.loaded }

// Path 返回从根开始的路径，用于日志展示。
func (n *Node) Path() string {
	if n.parent == nil {
		if n.isRoot {
			return "我的云盘"
		}
		return n.file.Filename
	}
	return n.parent.Path() + "/" + n.file.Filename
}

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

// ApplyFolder 用最新的目录内容更新节点（保留已有子节点指针），
// 然后通知树控件刷新该节点的子树。必须在 UI 线程调用。
func (m *TreeModel) ApplyFolder(node *Node, files []api.FileInfo) {
	if node == nil || !node.IsDir() {
		return
	}

	old := make(map[int64]*Node, len(node.children))
	for _, c := range node.children {
		old[c.file.FileID] = c
	}

	children := make([]*Node, 0, len(files))
	for _, f := range files {
		if c, ok := old[f.FileID]; ok {
			delete(old, f.FileID)
			if c.file.IsDir() != f.IsDir() {
				// 类型发生变化（极端情况），丢弃旧的子树。
				c.children = nil
				c.loaded = false
			}
			c.file = f
			children = append(children, c)
			continue
		}
		children = append(children, &Node{parent: node, file: f})
	}

	for _, c := range children {
		c.ensurePlaceholder()
	}

	node.children = children
	node.loaded = true

	m.PublishItemsReset(node)
}
