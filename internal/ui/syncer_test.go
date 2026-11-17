package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"pan123/internal/store"
	"pan123/internal/webdav"
)

// fakeDAV 是一个极简的 WebDAV 服务：按路径返回 PROPFIND 207 响应。
type fakeDAV struct {
	baseURL string

	mu     sync.Mutex
	tree   map[string][]store.Entry
	calls  map[string]int
	status int            // 非 0 时所有请求直接返回该状态码
	fail   map[string]int // 指定目录强制返回某个状态码（模拟单个目录失效）
}

func newFakeDAV(t *testing.T, tree map[string][]store.Entry) *fakeDAV {
	t.Helper()

	f := &fakeDAV{
		tree:  tree,
		calls: make(map[string]int),
		fail:  make(map[string]int),
	}

	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	f.baseURL = srv.URL + "/dav"

	return f
}

func (f *fakeDAV) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PROPFIND" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	dir := store.NormalizePath(strings.TrimPrefix(r.URL.Path, "/dav"))

	f.mu.Lock()
	f.calls[dir]++
	entries := f.tree[dir]
	status := f.status
	if code, ok := f.fail[dir]; ok {
		status = code
	}
	f.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		return
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)

	// 目录自身
	fmt.Fprintf(&b, `<D:response><D:href>%s</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`,
		xmlEscape(davHref("/dav", dir, "", true)))

	for _, e := range entries {
		href := davHref("/dav", dir, e.Name, e.IsDir)
		b.WriteString(`<D:response><D:href>`)
		b.WriteString(xmlEscape(href))
		b.WriteString(`</D:href><D:propstat><D:prop>`)
		if e.IsDir {
			b.WriteString(`<D:resourcetype><D:collection/></D:resourcetype>`)
		} else {
			b.WriteString(`<D:resourcetype/>`)
		}
		fmt.Fprintf(&b, `<D:getcontentlength>%d</D:getcontentlength>`, e.Size)
		if e.ETag != "" {
			fmt.Fprintf(&b, `<D:getetag>%q</D:getetag>`, e.ETag)
		}
		b.WriteString(`</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`)
	}

	b.WriteString(`</D:multistatus>`)

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write([]byte(b.String()))
}

func (f *fakeDAV) callCount(dir string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[dir]
}

func (f *fakeDAV) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// davHref 拼出 WebDAV 响应里的 href（各段做 URL 转义）。
func davHref(base, dir, name string, isDir bool) string {
	segs := []string{strings.Trim(base, "/")}
	if d := strings.Trim(dir, "/"); d != "" {
		segs = append(segs, strings.Split(d, "/")...)
	}
	if name != "" {
		segs = append(segs, name)
	}
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	href := "/" + strings.Join(segs, "/")
	if isDir && !strings.HasSuffix(href, "/") {
		href += "/"
	}
	return href
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;").Replace(s)
}

func newTestSyncer(t *testing.T, f *fakeDAV) (*Syncer, *store.Cache, *TreeModel, *Node) {
	t.Helper()

	cache, err := store.Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { cache.Close() })

	client := webdav.New(webdav.Options{BaseURL: f.baseURL, QPS: 1000})

	root := NewRootNode()
	model := NewTreeModel(root)

	s := NewSyncer(client, cache)
	s.apply = func(n *Node, entries []store.Entry) { model.ApplyFolder(n, entries) }
	s.logf = func(format string, args ...any) {}
	s.status = func(string) {}
	s.busy = func(bool) {}

	return s, cache, model, root
}

// TestSyncerRecursive 验证「选中目录 -> 递归同步 -> 写入 SQLite -> 更新树」的完整流程。
func TestSyncerRecursive(t *testing.T) {
	f := newFakeDAV(t, map[string][]store.Entry{
		"/": {
			{Name: "电影", IsDir: true},
			{Name: "root.txt", Size: 123, ETag: "r1"},
		},
		"/电影": {
			{Name: "2024", IsDir: true},
			{Name: "movie.mp4", Size: 1024},
		},
		"/电影/2024": {},
	})

	s, cache, _, root := newTestSyncer(t, f)
	s.process(&Job{Node: root, Recursive: true})

	for _, dir := range []string{"/", "/电影", "/电影/2024"} {
		if got := f.callCount(dir); got != 1 {
			t.Fatalf("calls[%s] = %d, want 1", dir, got)
		}
	}

	if got := root.ChildCount(); got != 2 {
		t.Fatalf("root children = %d, want 2", got)
	}
	if !root.Loaded() {
		t.Fatal("root should be loaded")
	}

	dir := root.children[0]
	if !dir.IsDir() || !dir.Loaded() || dir.ChildCount() != 2 {
		t.Fatalf("dir = %+v", dir)
	}
	if dir.Path() != "/电影" {
		t.Fatalf("dir path = %q", dir.Path())
	}

	year := dir.children[0]
	if year.Path() != "/电影/2024" || !year.Loaded() || year.ChildCount() != 0 {
		t.Fatalf("year = %+v", year)
	}

	// SQLite 缓存已写入，且空目录也标记为已同步。
	if entries, ok := cache.Children("/"); !ok || len(entries) != 2 {
		t.Fatalf("cache root = %v, ok=%v", entries, ok)
	}
	if entries, ok := cache.Children("/电影"); !ok || len(entries) != 2 {
		t.Fatalf("cache dir = %v, ok=%v", entries, ok)
	}
	if entries, ok := cache.Children("/电影/2024"); !ok || len(entries) != 0 {
		t.Fatalf("cache empty dir = %v, ok=%v", entries, ok)
	}
}

// TestSyncSelectedOnlyOneLevel 验证「不包含子目录」时只请求一层。
func TestSyncSelectedOnlyOneLevel(t *testing.T) {
	f := newFakeDAV(t, map[string][]store.Entry{
		"/": {{Name: "dir", IsDir: true}, {Name: "f.txt", Size: 1}},
	})

	s, _, _, root := newTestSyncer(t, f)
	s.process(&Job{Node: root, Recursive: false})

	if got := f.totalCalls(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	if root.ChildCount() != 2 {
		t.Fatalf("root children = %d", root.ChildCount())
	}
	dir := root.children[0]
	if dir.Loaded() {
		t.Fatal("sub dir should stay unloaded when not recursive")
	}
	if !dir.HasChild() {
		t.Fatal("unloaded dir should report HasChild() == true")
	}
}

// TestSyncerAuthErrorKeepsCache 验证鉴权失败时不会写入缓存。
func TestSyncerAuthErrorKeepsCache(t *testing.T) {
	f := newFakeDAV(t, map[string][]store.Entry{"/": {{Name: "x"}}})
	f.status = http.StatusUnauthorized

	s, cache, _, root := newTestSyncer(t, f)
	s.process(&Job{Node: root, Recursive: true})

	if _, ok := cache.Children("/"); ok {
		t.Fatal("cache should not be written on auth error")
	}
	if root.Loaded() {
		t.Fatal("root should not be marked loaded on auth error")
	}
}

// TestSyncerOneFolderFailsKeepsGoing 验证单个目录 404 不会中断整体同步。
func TestSyncerOneFolderFailsKeepsGoing(t *testing.T) {
	f := newFakeDAV(t, map[string][]store.Entry{
		"/": {
			{Name: "ok", IsDir: true},
			{Name: "bad", IsDir: true},
			{Name: "f.txt", Size: 1},
		},
		"/ok": {{Name: "inner.txt"}},
	})
	f.fail["/bad"] = http.StatusNotFound

	s, cache, _, root := newTestSyncer(t, f)
	s.process(&Job{Node: root, Recursive: true})

	if entries, ok := cache.Children("/"); !ok || len(entries) != 3 {
		t.Fatalf("cache root = %v, ok=%v", entries, ok)
	}
	if entries, ok := cache.Children("/ok"); !ok || len(entries) != 1 {
		t.Fatalf("cache /ok = %v, ok=%v", entries, ok)
	}
	if _, ok := cache.Children("/bad"); ok {
		t.Fatal("failed folder must not be cached")
	}

	okDir := childByName(root, "ok")
	badDir := childByName(root, "bad")
	if okDir == nil || badDir == nil {
		t.Fatalf("children = %v", root.children)
	}
	if !okDir.Loaded() || okDir.ChildCount() != 1 {
		t.Fatalf("ok dir = %+v", okDir)
	}
	if badDir.Loaded() {
		t.Fatal("failed dir should stay unloaded")
	}
}

func childByName(n *Node, name string) *Node {
	for _, c := range n.children {
		if c.entry.Name == name {
			return c
		}
	}
	return nil
}

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		1023:    "1023 B",
		1024:    "1.0 KB",
		1536:    "1.5 KB",
		1 << 20: "1.0 MB",
		1 << 30: "1.0 GB",
	}
	for in, want := range cases {
		if got := FormatSize(in); got != want {
			t.Fatalf("FormatSize(%d) = %q, want %q", in, got, want)
		}
	}
}
