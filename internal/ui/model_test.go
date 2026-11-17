package ui

import (
	"testing"

	"pan123/internal/api"
)

// TestPlaceholderForUnloadedFolder 验证「未加载的文件夹」会带一个占位子项，
// 否则 TreeView 无法展开它（从而懒加载/展开自动同步不会触发）。
func TestPlaceholderForUnloadedFolder(t *testing.T) {
	root := NewRootNode()
	if root.ChildCount() != 1 || !root.children[0].placeholder {
		t.Fatalf("root should start with a placeholder child, got %#v", root.children)
	}

	model := NewTreeModel(root)
	model.ApplyFolder(root, []api.FileInfo{
		{FileID: 10, Filename: "dir", Type: 1},
		{FileID: 11, Filename: "file.txt", Type: 0, Size: 3},
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
	if got := dir.ChildCount(); got != 1 || !dir.children[0].placeholder {
		t.Fatalf("unloaded dir should have exactly one placeholder child, got %d", got)
	}
	if txt := dir.ChildAt(0).Text(); txt == "" {
		t.Fatal("placeholder item text must not be empty")
	}

	file := root.children[1]
	if file.IsDir() {
		t.Fatalf("second child should be a file")
	}
	if file.ChildCount() != 0 || file.HasChild() {
		t.Fatal("file should have no children and no expander")
	}

	// 同步该目录后：占位子项被真实子项替换。
	model.ApplyFolder(dir, []api.FileInfo{{FileID: 12, Filename: "inner.txt", Type: 0, Size: 1}})
	if !dir.Loaded() || dir.ChildCount() != 1 {
		t.Fatalf("dir after apply: loaded=%v children=%d", dir.Loaded(), dir.ChildCount())
	}
	if dir.children[0].placeholder || dir.children[0].FileID() != 12 {
		t.Fatalf("placeholder was not replaced: %#v", dir.children[0])
	}

	// 空的已加载目录不应再显示展开箭头。
	model.ApplyFolder(dir, nil)
	if dir.ChildCount() != 0 || dir.HasChild() {
		t.Fatalf("empty loaded dir: children=%d HasChild=%v", dir.ChildCount(), dir.HasChild())
	}
}

// TestPlaceholderKeptWhenSyncFails 验证同步失败（未 apply）时占位子项仍在。
func TestPlaceholderKept(t *testing.T) {
	root := NewRootNode()
	model := NewTreeModel(root)
	model.ApplyFolder(root, []api.FileInfo{{FileID: 10, Filename: "dir", Type: 1}})

	dir := root.children[0]
	if !dir.children[0].placeholder {
		t.Fatal("expected placeholder")
	}
	// 模拟再次展开、仍然没同步成功：占位子项不会被清掉。
	dir.ensurePlaceholder()
	if dir.ChildCount() != 1 || !dir.children[0].placeholder {
		t.Fatalf("children = %#v", dir.children)
	}
}

// TestNodePath 验证路径拼接使用原始文件名（不包含大小等展示后缀）。
func TestNodePath(t *testing.T) {
	root := NewRootNode()
	model := NewTreeModel(root)
	model.ApplyFolder(root, []api.FileInfo{{FileID: 10, Filename: "dir", Type: 1}})
	dir := root.children[0]
	model.ApplyFolder(dir, []api.FileInfo{{FileID: 11, Filename: "a.bin", Type: 0, Size: 2048}})

	file := dir.children[0]
	if got := file.Path(); got != "我的云盘/dir/a.bin" {
		t.Fatalf("path = %q", got)
	}
	if got := dir.Path(); got != "我的云盘/dir" {
		t.Fatalf("dir path = %q", got)
	}
}
