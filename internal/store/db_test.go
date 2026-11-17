package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
	created := time.Date(2024, 12, 31, 8, 30, 0, 0, time.UTC)
	err = c.SetFolder("/", []Entry{
		{Name: "meta.txt", Size: 10, ETag: "e1", Created: created, Modified: modified, ContentType: "text/plain"},
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
	if !meta.Created.Equal(created) {
		t.Fatalf("created = %s, want %s", meta.Created, created)
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

// TestSearch 验证本地模糊搜索：忽略大小写、子串匹配、多关键字、通配符转义、排序。
func TestSearch(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.SetFolder("/", []Entry{
		{Name: "Movies", IsDir: true},
		{Name: "The Movie Collection.mp4", Size: 100},
		{Name: "报告2025.pdf", Size: 200},
		{Name: "README.md", Size: 300},
		{Name: "100%_done.txt", Size: 400},
	}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := c.SetFolder("/Movies", []Entry{
		{Name: "Interstellar.mkv", Size: 500},
		{Name: "inception.MKV", Size: 600},
	}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}

	// 忽略大小写的子串匹配 + 相关度排序（前缀优先）。
	hits, err := c.Search("movie", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %v", hitNames(hits))
	}
	if hits[0].Name != "Movies" || !hits[0].IsDir {
		t.Fatalf("first hit = %+v, want Movies (prefix match)", hits[0])
	}
	if hits[1].Name != "The Movie Collection.mp4" {
		t.Fatalf("second hit = %+v", hits[1])
	}

	// 子目录里的文件也要能搜到，并带完整路径。
	hits, err = c.Search("INCEPTION", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %v", hitNames(hits))
	}
	if hits[0].Path != "/Movies/inception.MKV" || hits[0].Parent != "/Movies" {
		t.Fatalf("hit = %+v", hits[0])
	}

	// 中文搜索。
	hits, _ = c.Search("报告", 20)
	if len(hits) != 1 || hits[0].Name != "报告2025.pdf" {
		t.Fatalf("hits = %v", hitNames(hits))
	}

	// 空格分隔的多关键字（AND）。
	hits, _ = c.Search("报告 PDF", 20)
	if len(hits) != 1 || hits[0].Name != "报告2025.pdf" {
		t.Fatalf("multi-term hits = %v", hitNames(hits))
	}
	hits, _ = c.Search("报告 docx", 20)
	if len(hits) != 0 {
		t.Fatalf("multi-term hits = %v, want none", hitNames(hits))
	}

	// LIKE 通配符必须被转义：% 只匹配字面量 %。
	hits, _ = c.Search("%", 20)
	if len(hits) != 1 || hits[0].Name != "100%_done.txt" {
		t.Fatalf("wildcard hits = %v", hitNames(hits))
	}
	hits, _ = c.Search("_done", 20)
	if len(hits) != 1 || hits[0].Name != "100%_done.txt" {
		t.Fatalf("underscore hits = %v", hitNames(hits))
	}

	// 空查询 / 无匹配。
	if hits, err := c.Search("   ", 20); err != nil || len(hits) != 0 {
		t.Fatalf("empty query = %v, %v", hitNames(hits), err)
	}
	if hits, err := c.Search("不存在的名字", 20); err != nil || len(hits) != 0 {
		t.Fatalf("no-match query = %v, %v", hitNames(hits), err)
	}

	// limit 生效。
	hits, _ = c.Search("e", 2)
	if len(hits) != 2 {
		t.Fatalf("limit not applied: %d", len(hits))
	}
}

func hitNames(hits []SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Path
	}
	return out
}

// TestNoSidecarJournalFiles 验证使用 DELETE 日志模式：运行期间和退出后都不会留下 -wal/-shm。
func TestNoSidecarJournalFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tree.db")

	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var mode string
	if err := c.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("journal_mode = %q, want delete", mode)
	}

	if err := c.SetFolder("/", []Entry{{Name: "a.txt", Size: 1}}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("unexpected sidecar file %s (err=%v)", path+suffix, err)
		}
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s left after close (err=%v)", path+suffix, err)
		}
	}
}

// TestMigratesFromWAL 验证老版本 WAL 数据库打开后会切回 DELETE 模式且数据不丢。
func TestMigratesFromWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tree.db")

	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO folder(path, synced_at) VALUES('/', ?)`, time.Now().UnixNano()); err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO entry(parent, name, sort_name, is_dir, size) VALUES('/', '老数据.txt', '老数据.txt', 0, 42)`); err != nil {
		t.Fatalf("insert entry: %v", err)
	}
	var rawMode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&rawMode); err != nil || !strings.EqualFold(rawMode, "wal") {
		t.Fatalf("raw journal_mode = %q, %v", rawMode, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	var mode string
	if err := c.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "delete") {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	entries, ok := c.Children("/")
	if !ok || len(entries) != 1 || entries[0].Name != "老数据.txt" || entries[0].Size != 42 {
		t.Fatalf("entries = %+v, ok=%v", entries, ok)
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("wal file left behind (err=%v)", err)
	}
	if _, err := os.Stat(path + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("shm file left behind (err=%v)", err)
	}
}

// TestMigratesMissingCreatedColumn 验证老数据库（没有 created 列）会自动加列且数据不丢。
func TestMigratesMissingCreatedColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tree.db")

	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	legacy := `
CREATE TABLE folder (path TEXT PRIMARY KEY, synced_at INTEGER NOT NULL);
CREATE TABLE entry (
	parent TEXT NOT NULL, name TEXT NOT NULL, sort_name TEXT NOT NULL,
	is_dir INTEGER NOT NULL, size INTEGER NOT NULL,
	etag TEXT NOT NULL DEFAULT '', modified INTEGER NOT NULL DEFAULT 0,
	content_type TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (parent, name)
);`
	if _, err := raw.Exec(legacy); err != nil {
		t.Fatalf("legacy schema: %v", err)
	}
	modified := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano()
	if _, err := raw.Exec(`INSERT INTO folder(path, synced_at) VALUES('/', 1)`); err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO entry(parent, name, sort_name, is_dir, size, etag, modified, content_type)
		VALUES('/', 'old.txt', 'old.txt', 0, 7, 'e', ?, 'text/plain')`, modified); err != nil {
		t.Fatalf("insert entry: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	entries, ok := c.Children("/")
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %v, ok=%v", entries, ok)
	}
	e := entries[0]
	if e.Name != "old.txt" || e.Size != 7 || e.ETag != "e" {
		t.Fatalf("entry = %+v", e)
	}
	if !e.Modified.Equal(time.Unix(0, modified)) {
		t.Fatalf("modified = %s", e.Modified)
	}
	if !e.Created.IsZero() {
		t.Fatalf("created = %s, want zero", e.Created)
	}

	// 新列可以正常写入。
	if err := c.SetFolder("/", []Entry{{Name: "new.txt", Created: time.Now()}}); err != nil {
		t.Fatalf("SetFolder after migration: %v", err)
	}
}

// TestDeleteEntryAndSubtree 验证删除文件与目录（含子树）的缓存清理。
func TestDeleteEntryAndSubtree(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.SetFolder("/", []Entry{{Name: "dir", IsDir: true}, {Name: "a.txt"}}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := c.SetFolder("/dir", []Entry{{Name: "sub", IsDir: true}, {Name: "b.txt"}}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := c.SetFolder("/dir/sub", []Entry{{Name: "c.txt"}}); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}

	// 删除单个文件：父目录少一项，其它目录不受影响。
	if err := c.Delete("/dir/b.txt"); err != nil {
		t.Fatalf("Delete file: %v", err)
	}
	children, ok := c.Children("/dir")
	if !ok || len(children) != 1 || children[0].Name != "sub" {
		t.Fatalf("children = %v, ok=%v", children, ok)
	}

	// 删除目录：整棵子树都清掉。
	if err := c.Delete("/dir"); err != nil {
		t.Fatalf("Delete dir: %v", err)
	}
	root, ok := c.Children("/")
	if !ok || len(root) != 1 || root[0].Name != "a.txt" {
		t.Fatalf("root = %v, ok=%v", root, ok)
	}
	if _, ok := c.Children("/dir"); ok {
		t.Fatal("/dir should be gone")
	}
	if _, ok := c.Children("/dir/sub"); ok {
		t.Fatal("/dir/sub should be gone")
	}
	if _, ok := c.SyncedAt("/dir/sub"); ok {
		t.Fatal("/dir/sub synced_at should be gone")
	}

	// 删除不存在的条目不是错误；根目录不允许删除。
	if err := c.Delete("/nope.txt"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
	if err := c.Delete("/"); err == nil {
		t.Fatal("deleting root cache should fail")
	}
}

// TestDuplicateFiles 验证同名文件检测：忽略大小写、只统计文件、按名字分组。
func TestDuplicateFiles(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	created := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	modified := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)

	if err := c.SetFolder("/", []Entry{
		{Name: "a.txt", Size: 100, Created: created, Modified: modified},
		{Name: "only.txt", Size: 1},
		{Name: "Same.txt", Size: 2},
		{Name: "docs", IsDir: true},
	}); err != nil {
		t.Fatalf("SetFolder /: %v", err)
	}
	if err := c.SetFolder("/docs", []Entry{
		{Name: "a.txt", Size: 200},
		{Name: "same.TXT", Size: 3},
		{Name: "another", IsDir: true},
	}); err != nil {
		t.Fatalf("SetFolder /docs: %v", err)
	}
	// 与 "/docs" 同名的文件夹不算重复；文件夹里的 a.txt 仍要参与统计。
	if err := c.SetFolder("/other", []Entry{
		{Name: "docs", IsDir: true},
		{Name: "a.txt", Size: 300},
	}); err != nil {
		t.Fatalf("SetFolder /other: %v", err)
	}
	if err := c.SetFolder("/other/docs", []Entry{
		{Name: "same.txt", Size: 4},
	}); err != nil {
		t.Fatalf("SetFolder /other/docs: %v", err)
	}

	groups, err := c.DuplicateFiles(2, 100)
	if err != nil {
		t.Fatalf("DuplicateFiles: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2 (%+v)", len(groups), groups)
	}

	a := groups[0]
	if a.Name != "a.txt" || len(a.Files) != 3 {
		t.Fatalf("group 0 = %+v", a)
	}
	if got, want := hitNames(a.Files), []string{"/a.txt", "/docs/a.txt", "/other/a.txt"}; !equalStrings(got, want) {
		t.Fatalf("group 0 files = %v, want %v", got, want)
	}
	if a.Files[0].Parent != "/" || a.Files[1].Parent != "/docs" {
		t.Fatalf("group 0 parents = %+v", a.Files)
	}
	if !a.Files[0].Created.Equal(created) || !a.Files[0].Modified.Equal(modified) || a.Files[0].Size != 100 {
		t.Fatalf("group 0 first file = %+v", a.Files[0])
	}

	// 大小写不同的同名文件归到一组，组名取第一次遇到的名字。
	same := groups[1]
	if len(same.Files) != 3 {
		t.Fatalf("group 1 = %+v", same)
	}
	names := make(map[string]bool, 3)
	for _, f := range same.Files {
		names[f.Name] = true
	}
	if !names["Same.txt"] || !names["same.TXT"] || !names["same.txt"] {
		t.Fatalf("group 1 names = %v", names)
	}

	// min 大于组内文件数时不应返回结果。
	groups, err = c.DuplicateFiles(4, 100)
	if err != nil {
		t.Fatalf("DuplicateFiles(min=4): %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want none", groups)
	}

	// limit 限制组数。
	groups, err = c.DuplicateFiles(2, 1)
	if err != nil {
		t.Fatalf("DuplicateFiles(limit=1): %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want 1", groups)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
