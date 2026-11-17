package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"pan123/internal/api"
	"pan123/internal/store"
)

// TestSyncerRecursive 用假接口验证「选中目录 -> 递归同步 -> 写入缓存 -> 更新树」的完整流程。
func TestSyncerRecursive(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/file/list" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Platform"); got != "open_platform" {
			t.Errorf("Platform = %q", got)
		}

		parent := r.URL.Query().Get("parentFileId")
		mu.Lock()
		calls[parent]++
		mu.Unlock()

		var body string
		switch parent {
		case "0":
			body = `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[
				{"fileId":20,"filename":"sub","type":1,"size":0,"parentFileId":0,"trashed":0,"updateAt":"2025-01-01 10:00:00"},
				{"fileId":30,"filename":"root.txt","type":0,"size":123,"parentFileId":0,"trashed":0,"updateAt":"2025-01-02 10:00:00"},
				{"fileId":31,"filename":"deleted.txt","type":0,"size":1,"parentFileId":0,"trashed":1}]}}`
		case "20":
			body = `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[
				{"fileId":21,"filename":"inner.bin","type":0,"size":4096,"parentFileId":20,"trashed":0}]}}`
		default:
			t.Errorf("unexpected parentFileId: %s", parent)
			body = `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[]}}`
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	cache, err := store.Open(filepath.Join(t.TempDir(), "tree.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	client := api.New(api.Options{BaseURL: srv.URL, AccessToken: "test-token", QPS: 1000})
	root := NewRootNode()
	model := NewTreeModel(root)

	logs := 0
	s := NewSyncer(client, cache)
	s.apply = func(n *Node, files []api.FileInfo) { model.ApplyFolder(n, files) }
	s.logf = func(format string, args ...any) { logs++ }
	s.status = func(string) {}
	s.busy = func(bool) {}

	s.process(&Job{Node: root, Recursive: true})

	mu.Lock()
	defer mu.Unlock()
	if calls["0"] != 1 || calls["20"] != 1 {
		t.Fatalf("calls = %v, want 1 call for 0 and 20", calls)
	}

	// 树被更新：根目录 2 项（回收站条目被过滤），子目录 1 项。
	if got := root.ChildCount(); got != 2 {
		t.Fatalf("root children = %d, want 2", got)
	}
	if !root.Loaded() {
		t.Fatal("root should be loaded")
	}

	sub, ok := root.ChildAt(0).(*Node)
	if !ok || !sub.IsDir() {
		t.Fatalf("first child should be sub dir, got %#v", root.ChildAt(0))
	}
	if got := sub.ChildCount(); got != 1 {
		t.Fatalf("sub children = %d, want 1", got)
	}
	if file := sub.ChildAt(0).(*Node); file.File().FileID != 21 || file.File().Size != 4096 {
		t.Fatalf("inner file = %+v", file.File())
	}

	// 缓存已写入。
	if files, ok := cache.Children(0); !ok || len(files) != 2 {
		t.Fatalf("cache root = %v, %v", files, ok)
	}
	if files, ok := cache.Children(20); !ok || len(files) != 1 {
		t.Fatalf("cache sub = %v, %v", files, ok)
	}
	if logs == 0 {
		t.Fatal("expected some log output")
	}
}

// TestSyncSelectedOnlyOneLevel 验证「不包含子目录」时只请求一层。
func TestSyncSelectedOnlyOneLevel(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[
			{"fileId":20,"filename":"sub","type":1,"size":0,"trashed":0}]}}`)
	}))
	defer srv.Close()

	cache, err := store.Open(filepath.Join(t.TempDir(), "tree.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	client := api.New(api.Options{BaseURL: srv.URL, AccessToken: "t", QPS: 1000})
	root := NewRootNode()
	model := NewTreeModel(root)

	s := NewSyncer(client, cache)
	s.apply = func(n *Node, files []api.FileInfo) { model.ApplyFolder(n, files) }
	s.logf = func(format string, args ...any) {}
	s.status = func(string) {}
	s.busy = func(bool) {}

	s.process(&Job{Node: root, Recursive: false})

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if root.ChildCount() != 1 {
		t.Fatalf("root children = %d, want 1", root.ChildCount())
	}
	sub := root.ChildAt(0).(*Node)
	if sub.Loaded() {
		t.Fatal("sub should stay unloaded when not recursive")
	}
	if !sub.HasChild() {
		t.Fatal("unloaded dir should report HasChild() == true so the tree shows an expander")
	}
}

// TestSyncerServerErrorKeepsCache 验证接口失败时不会写入缓存。
func TestSyncerServerErrorKeepsCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":5113,"message":"参数错误"}`)
	}))
	defer srv.Close()

	cache, err := store.Open(filepath.Join(t.TempDir(), "tree.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	client := api.New(api.Options{BaseURL: srv.URL, AccessToken: "t", QPS: 1000})
	root := NewRootNode()
	model := NewTreeModel(root)

	s := NewSyncer(client, cache)
	s.apply = func(n *Node, files []api.FileInfo) { model.ApplyFolder(n, files) }
	s.logf = func(format string, args ...any) {}
	s.status = func(string) {}
	s.busy = func(bool) {}

	s.process(&Job{Node: root, Recursive: true})

	if _, ok := cache.Children(0); ok {
		t.Fatal("cache should not be written on API error")
	}
	if root.Loaded() {
		t.Fatal("root should not be marked as loaded on API error")
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		1023:    "1023 B",
		1024:    "1.0 KB",
		1536:    "1.5 KB",
		1048576: "1.0 MB",
		1 << 30: "1.0 GB",
	}
	for in, want := range cases {
		if got := FormatSize(in); got != want {
			t.Fatalf("FormatSize(%d) = %q, want %q", in, got, want)
		}
	}
}
