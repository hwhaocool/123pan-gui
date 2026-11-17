package webdav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"pan123/internal/store"
)

const listXML = `<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/dav/123/</D:href>
    <D:propstat>
      <D:prop><D:displayname>123</D:displayname><D:resourcetype><D:collection/></D:resourcetype></D:prop>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/123/%E7%94%B5%E5%BD%B1/</D:href>
    <D:propstat>
      <D:prop><D:resourcetype><D:collection/></D:resourcetype><D:getlastmodified>Sat, 01 Mar 2025 12:00:00 GMT</D:getlastmodified></D:prop>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/123/report%20final.pdf</D:href>
    <D:propstat>
      <D:prop>
        <D:resourcetype/>
        <D:getcontentlength>2411724</D:getcontentlength>
        <D:getetag>"abc123"</D:getetag>
        <D:getcontenttype>application/pdf</D:getcontenttype>
        <D:getlastmodified>Sat, 01 Mar 2025 08:30:00 GMT</D:getlastmodified>
      </D:prop>
      <D:status>HTTP/1.1 200 OK</D:status>
    </D:propstat>
  </D:response>
</D:multistatus>`

func TestListParsesMultistatus(t *testing.T) {
	var (
		mu     sync.Mutex
		method string
		depth  string
		target string
		auth   string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		method = r.Method
		depth = r.Header.Get("Depth")
		target = r.URL.EscapedPath()
		user, pass, ok := r.BasicAuth()
		if ok {
			auth = user + ":" + pass
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(listXML))
	}))
	defer srv.Close()

	c := New(Options{
		BaseURL:  srv.URL + "/dav/123",
		Username: "user",
		Password: "pass",
		QPS:      1000,
	})

	entries, err := c.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	mu.Lock()
	if method != "PROPFIND" {
		t.Fatalf("method = %q", method)
	}
	if depth != "1" {
		t.Fatalf("Depth = %q", depth)
	}
	if target != "/dav/123/" {
		t.Fatalf("request path = %q, want /dav/123/", target)
	}
	if auth != "user:pass" {
		t.Fatalf("basic auth = %q", auth)
	}
	mu.Unlock()

	if len(entries) != 2 {
		t.Fatalf("entries = %d (%v), want 2 (self must be skipped)", len(entries), entryNames(entries))
	}

	dir := entries[0]
	if dir.Name != "电影" || !dir.IsDir {
		t.Fatalf("dir = %+v", dir)
	}

	file := entries[1]
	if file.Name != "report final.pdf" {
		t.Fatalf("file name = %q", file.Name)
	}
	if file.IsDir || file.Size != 2411724 {
		t.Fatalf("file = %+v", file)
	}
	if file.ETag != "abc123" {
		t.Fatalf("etag = %q", file.ETag)
	}
	if file.ContentType != "application/pdf" {
		t.Fatalf("content type = %q", file.ContentType)
	}
	if file.Modified.IsZero() || file.Modified.UTC().Format(time.RFC3339) != "2025-03-01T08:30:00Z" {
		t.Fatalf("modified = %s", file.Modified)
	}
}

func TestListUsesEscapedPath(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<D:multistatus xmlns:D="DAV:"></D:multistatus>`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, QPS: 1000})
	if _, err := c.List(context.Background(), "/电影/2024"); err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotPath != "/%E7%94%B5%E5%BD%B1/2024/" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestCheck(t *testing.T) {
	var depth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		depth = r.Header.Get("Depth")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<D:multistatus xmlns:D="DAV:"><D:response><D:href>/</D:href></D:response></D:multistatus>`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, QPS: 1000})
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if depth != "0" {
		t.Fatalf("Depth = %q, want 0", depth)
	}
}

func TestAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, QPS: 1000})
	_, err := c.List(context.Background(), "/")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsAuthError(err) {
		t.Fatalf("err = %v, want AuthError", err)
	}
}

func TestNotWebDAV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, QPS: 1000})
	_, err := c.List(context.Background(), "/")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "不是 WebDAV") {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryOnServerError(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()

		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<D:multistatus xmlns:D="DAV:"><D:response><D:href>/x.txt</D:href></D:response></D:multistatus>`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, QPS: 1000})
	entries, err := c.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entryNames(entries))
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// TestRedirectKeepsMethod 验证手动跟随重定向时仍然使用 PROPFIND（Go 默认会改成 GET）。
func TestRedirectKeepsMethod(t *testing.T) {
	var (
		mu      sync.Mutex
		methods []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()

		if r.URL.Path == "/dav" {
			http.Redirect(w, r, "/dav/123/", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<D:multistatus xmlns:D="DAV:"></D:multistatus>`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL + "/dav", QPS: 1000})
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 2 {
		t.Fatalf("requests = %v", methods)
	}
	for _, m := range methods {
		if m != "PROPFIND" {
			t.Fatalf("methods = %v, want PROPFIND for all hops", methods)
		}
	}
}

func TestStat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Depth") != "0" {
			t.Errorf("Depth = %q", r.Header.Get("Depth"))
		}
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/a%20b.txt</D:href>
    <D:propstat><D:prop><D:resourcetype/><D:getcontentlength>42</D:getcontentlength></D:prop>
      <D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
</D:multistatus>`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, QPS: 1000})
	e, err := c.Stat(context.Background(), "/a b.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if e.Name != "a b.txt" || e.Size != 42 || e.IsDir {
		t.Fatalf("entry = %+v", e)
	}
}

func TestNotConfigured(t *testing.T) {
	c := New(Options{QPS: 1000})
	if c.Configured() {
		t.Fatal("empty base URL should not be configured")
	}
	if err := c.Check(context.Background()); err != ErrNotConfigured {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestConfigureNormalizesBaseURL(t *testing.T) {
	c := New(Options{BaseURL: "dav.example.com/dav/123/", QPS: 5})
	if got := c.BaseURL(); got != "https://dav.example.com/dav/123" {
		t.Fatalf("BaseURL = %q", got)
	}
	if c.QPS() != 5 {
		t.Fatalf("QPS = %v", c.QPS())
	}
}

func TestLimiterInterval(t *testing.T) {
	l := NewLimiter(2)
	if got := l.Interval(); got != 500*time.Millisecond {
		t.Fatalf("interval = %s", got)
	}
	l.SetQPS(1)
	if got := l.Interval(); got != time.Second {
		t.Fatalf("interval = %s", got)
	}
}

func entryNames(entries []store.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
