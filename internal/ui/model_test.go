package ui

import (
	"testing"

	"pan123/internal/store"
)

// TestMergeChildrenKeepsNodes 验证刷新目录时按名称复用子节点指针。
func TestMergeChildrenKeepsNodes(t *testing.T) {
	root := NewRootNode()
	root.mergeChildren([]store.Entry{
		{Name: "dir", IsDir: true},
		{Name: "a.txt", Size: 1},
	})
	if len(root.children) != 2 {
		t.Fatalf("children = %d", len(root.children))
	}
	dir := root.children[0]
	file := root.children[1]
	if dir.Path() != "/dir" || file.Path() != "/a.txt" {
		t.Fatalf("paths = %q, %q", dir.Path(), file.Path())
	}

	dir.mergeChildren([]store.Entry{{Name: "inner.txt", Size: 2}})
	inner := dir.children[0]
	if inner.Path() != "/dir/inner.txt" {
		t.Fatalf("inner path = %q", inner.Path())
	}

	root.mergeChildren([]store.Entry{
		{Name: "dir", IsDir: true, ETag: "changed"},
		{Name: "b.txt", Size: 3},
	})
	if len(root.children) != 2 {
		t.Fatalf("children = %d", len(root.children))
	}
	if root.children[0] != dir {
		t.Fatal("dir node pointer should be reused")
	}
	if dir.Entry().ETag != "changed" {
		t.Fatalf("entry not updated: %+v", dir.Entry())
	}
	if dir.children[0] != inner {
		t.Fatal("grandchildren should be untouched")
	}
	if root.children[1].Entry().Name != "b.txt" {
		t.Fatalf("new child = %+v", root.children[1].Entry())
	}
}

// TestOutlineChildren 验证目录树只显示文件夹，文件进入右侧列表。
func TestOutlineChildren(t *testing.T) {
	root := NewRootNode()
	root.mergeChildren([]store.Entry{
		{Name: "dir", IsDir: true},
		{Name: "empty", IsDir: true},
		{Name: "file.txt", Size: 1},
	})

	dir := root.children[0]
	empty := root.children[1]
	file := root.children[2]

	root.loaded = true

	// 根目录的文件也不会出现在目录树里。
	if got := root.outlineChildren(); got == nil || len(got) != 2 {
		t.Fatalf("root dirs = %#v", got)
	}
	if files := root.Files(); len(files) != 1 || files[0] != file {
		t.Fatalf("root files = %#v", files)
	}

	// 未加载的文件夹：非 nil 空切片（显示箭头，展开后触发加载）。
	if got := dir.outlineChildren(); got == nil || len(got) != 0 {
		t.Fatalf("unloaded dir children = %#v", got)
	}

	// 已加载的空文件夹：叶子。
	empty.loaded = true
	if got := empty.outlineChildren(); got != nil {
		t.Fatalf("empty loaded dir children = %#v", got)
	}

	// 已加载且有子文件夹 + 文件：树里只有子文件夹，文件进 Files。
	dir.loaded = true
	dir.mergeChildren([]store.Entry{
		{Name: "sub", IsDir: true},
		{Name: "inside.txt", Size: 2},
	})
	dirs := dir.outlineChildren()
	if len(dirs) != 1 || dirs[0].Name() != "sub" {
		t.Fatalf("dirs = %#v", dirs)
	}
	if files := dir.Files(); len(files) != 1 || files[0].Name() != "inside.txt" {
		t.Fatalf("files = %#v", files)
	}

	// 只有文件的已加载目录：不再显示展开箭头。
	dir.mergeChildren([]store.Entry{{Name: "only.txt", Size: 3}})
	if got := dir.outlineChildren(); got != nil {
		t.Fatalf("file-only dir children = %#v", got)
	}
	if files := dir.Files(); len(files) != 1 || files[0].Name() != "only.txt" {
		t.Fatalf("files = %#v", files)
	}
}

// TestFind 验证按路径查找节点。
func TestFind(t *testing.T) {
	root := NewRootNode()
	root.mergeChildren([]store.Entry{{Name: "dir", IsDir: true}, {Name: "a.txt"}})
	root.children[0].mergeChildren([]store.Entry{{Name: "inner", IsDir: true}})
	root.children[0].children[0].mergeChildren([]store.Entry{{Name: "deep.txt"}})

	if got := root.find("/"); got != root {
		t.Fatalf("find(/) = %v", got)
	}
	if got := root.find("/dir/inner/deep.txt"); got == nil || got.DisplayPath() != "我的云盘/dir/inner/deep.txt" {
		t.Fatalf("find deep = %v", got)
	}
	if got := root.find("/dir/nope"); got != nil {
		t.Fatalf("find missing = %v", got)
	}
	if got := root.find("/a.txt/x"); got != nil {
		t.Fatalf("find under file = %v", got)
	}
}
