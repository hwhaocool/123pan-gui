package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetFolderSortsAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tree.db")

	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if _, ok := c.Children("/"); ok {
		t.Fatal("fresh database should have no synced folders")
	}

	modified := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	err = c.SetFolder("/", []Entry{
		{Name: "meta.txt", Size: 10, ETag: "e1", Modified: modified, ContentType: "text/plain"},
		{Name: "A-Folder", IsDir: true},
		{Name: "a.txt", Size: 5},
		{Name: "z-folder", IsDir: true},
		{Name: "b.txt", Size: 11},
		{Name: "b.txt", Size: 12}, // 重复名称：后写入的覆盖前者
	})
	if err != nil {
		t.Fatalf("SetFolder: %v", err)
	}

	got, ok := c.Children("/")
	if !ok {
		t.Fatal("Children(/) should be ok after SetFolder")
	}

	wantOrder := []string{"A-Folder", "z-folder", "a.txt", "b.txt", "meta.txt"}
	if len(got) != len(wantOrder) {
		t.Fatalf("entries = %d, want %d (%v)", len(got), len(wantOrder), names(got))
	}
	for i := range wantOrder {
		if got[i].Name != wantOrder[i] {
			t.Fatalf("order = %v, want %v", names(got), wantOrder)
		}
	}

	if b := got[3]; b.Size != 12 {
		t.Fatalf("duplicate name should be overwritten, b.txt = %+v", b)
	}

	meta := got[4]
	if meta.Size != 10 || meta.ETag != "e1" || meta.ContentType != "text/plain" {
		t.Fatalf("meta.txt = %+v", meta)
	}
	if !meta.Modified.Equal(modified) {
		t.Fatalf("modified = %s, want %s", meta.Modified, modified)
	}

	if folders, items := c.Stats(); folders != 1 || items != 5 {
		t.Fatalf("stats = %d/%d, want 1/5", folders, items)
	}
	if c.LastSyncedAt().IsZero() {
		t.Fatal("LastSyncedAt is zero")
	}
	if _, ok := c.SyncedAt("/"); !ok {
		t.Fatal("SyncedAt(/) should be ok")
	}

	// 修改返回的切片不影响数据库。
	got[0].Name = "hacked"
	if again, _ := c.Children("/"); again[0].Name != "A-Folder" {
		t.Fatal("Children must return a copy")
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tree.db")

	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := c.SetFolder("/", []Entry{{Name: "dir", IsDir: true}}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := c.SetFolder("/dir", []Entry{{Name: "inner.txt", Size: 1024}}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	c2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { c2.Close() })

	if folders, items := c2.Stats(); folders != 2 || items != 2 {
		t.Fatalf("stats = %d/%d, want 2/2", folders, items)
	}
	children, ok := c2.Children("/dir")
	if !ok || len(children) != 1 || children[0].Name != "inner.txt" || children[0].Size != 1024 {
		t.Fatalf("children = %+v, ok=%v", children, ok)
	}
}

func TestEmptyFolderStaysSynced(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.SetFolder("/empty", nil); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	entries, ok := c.Children("/empty")
	if !ok {
		t.Fatal("empty folder should still be marked as synced")
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %v", entries)
	}
}

func TestRemove(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.SetFolder("/x", []Entry{{Name: "a"}}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := c.Remove("/x"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := c.Children("/x"); ok {
		t.Fatal("Children(/x) should be gone after Remove")
	}
	if _, ok := c.SyncedAt("/x"); ok {
		t.Fatal("SyncedAt(/x) should be gone after Remove")
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"":         "/",
		"/":        "/",
		"a/b":      "/a/b",
		"/a//b/":   "/a/b",
		" /x/y/ ":  "/x/y",
		"//a//b//": "/a/b",
		"/电影/2024": "/电影/2024",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Fatalf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tree.db")

	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not created: %v", err)
	}
	if c.Path() != path {
		t.Fatalf("Path() = %q, want %q", c.Path(), path)
	}
}

func names(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
