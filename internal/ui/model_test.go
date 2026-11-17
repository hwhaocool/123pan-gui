package ui

import (
	"strings"
	"testing"

	"pan123/internal/store"
)

// TestPlaceholderForUnloadedFolder 验证「未加载的文件夹」会带一个占位子项，
// 否则 TreeView 无法展开它（从而懒加载/展开自动同步不会触发）。
func TestPlaceholderForUnloadedFolder(t *testing.T) {
	root := NewRootNode()
	if root.ChildCount() != 1 || !root.children[0].placeholder {
		t.Fatalf("root should start with a placeholder child, got %#v", root.children)
	}
	if root.Path() != "/" {
		t.Fatalf("root path = %q", root.Path())
	}

	model := NewTreeModel(root)
	model.ApplyFolder(root, []store.Entry{
		{Name: "dir", IsDir: true},
		{Name: "file.txt", Size: 3},
	})

	if !root.Loaded() {
		t.Fatal("root should be loaded after ApplyFolder")
	}
	if got := root.ChildCount(); got != 2 {
		t.Fatalf("root children = %d, want 2", got)
	}

	dir := root.children[0]
	if !dir.IsDir() || dir.Loaded() {
		t.Fatalf("dir = %#v", dir)
	}
	if dir.Path() != "/dir" {
		t.Fatalf("dir path = %q", dir.Path())
	}
	if got := dir.ChildCount(); got != 1 || !dir.children[0].placeholder {
		t.Fatalf("unloaded dir should have exactly one placeholder child, got %d", got)
	}
	if txt := dir.ChildAt(0).Text(); txt == "" {
		t.Fatal("placeholder item text must not be empty")
	}

	file := root.children[1]
	if file.IsDir() || file.Path() != "/file.txt" {
		t.Fatalf("file = %#v path=%q", file, file.Path())
	}
	if file.ChildCount() != 0 || file.HasChild() {
		t.Fatal("file should have no children and no expander")
	}
	if txt := file.Text(); !strings.Contains(txt, "3 B") {
		t.Fatalf("file text = %q", txt)
	}

	// 同步该目录后：占位子项被真实子项替换。
	model.ApplyFolder(dir, []store.Entry{{Name: "inner.txt", Size: 1}})
	if !dir.Loaded() || dir.ChildCount() != 1 {
		t.Fatalf("dir after apply: loaded=%v children=%d", dir.Loaded(), dir.ChildCount())
	}
	inner := dir.children[0]
	if inner.placeholder || inner.Path() != "/dir/inner.txt" {
		t.Fatalf("inner = %#v", inner)
	}

	// 空的已加载目录不应再显示展开箭头。
	model.ApplyFolder(dir, nil)
	if dir.ChildCount() != 0 || dir.HasChild() {
		t.Fatalf("empty loaded dir: children=%d HasChild=%v", dir.ChildCount(), dir.HasChild())
	}
}

// TestApplyFolderKeepsNodes 验证按名称复用节点指针（walk 的 item↔handle 映射依赖它）。
func TestApplyFolderKeepsNodes(t *testing.T) {
	root := NewRootNode()
	model := NewTreeModel(root)

	model.ApplyFolder(root, []store.Entry{
		{Name: "keep", IsDir: true},
		{Name: "remove", IsDir: true},
	})
	keep := root.children[0]
	remove := root.children[1]
	if keep.entry.Name != "keep" || remove.entry.Name != "remove" {
		t.Fatalf("children = %v", []string{keep.entry.Name, remove.entry.Name})
	}

	model.ApplyFolder(root, []store.Entry{
		{Name: "keep", IsDir: true, ETag: "new-etag"},
		{Name: "added", Size: 10},
	})

	if len(root.children) != 2 {
		t.Fatalf("children = %d", len(root.children))
	}
	keptNode := root.children[0]
	if keptNode != keep {
		t.Fatal("existing node pointer should be reused")
	}
	if keptNode.Entry().ETag != "new-etag" {
		t.Fatalf("entry not updated: %+v", keptNode.Entry())
	}
	if root.children[1].Entry().Name != "added" {
		t.Fatalf("added child = %+v", root.children[1].Entry())
	}
}

// TestDisplayPath 验证日志用的展示路径。
func TestDisplayPath(t *testing.T) {
	root := NewRootNode()
	model := NewTreeModel(root)
	model.ApplyFolder(root, []store.Entry{{Name: "dir", IsDir: true}})
	dir := root.children[0]
	model.ApplyFolder(dir, []store.Entry{{Name: "a.bin", Size: 2048}})

	file := dir.children[0]
	if got := file.DisplayPath(); got != "我的云盘/dir/a.bin" {
		t.Fatalf("display path = %q", got)
	}
	if got := file.Path(); got != "/dir/a.bin" {
		t.Fatalf("webdav path = %q", got)
	}
}
