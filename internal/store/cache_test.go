package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"pan123/internal/api"
)

func TestSetFolderFiltersAndSorts(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "tree.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, ok := c.Children(0); ok {
		t.Fatal("new cache should not have children for 0")
	}

	skipped := c.SetFolder(0, []api.FileInfo{
		{FileID: 1, Filename: "b.txt"},
		{FileID: 2, Filename: "A-Folder", Type: 1},
		{FileID: 3, Filename: "a.txt"},
		{FileID: 4, Filename: "trash.bin", Trashed: 1},
		{FileID: 5, Filename: "z-folder", Type: 1},
	})

	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}

	got, ok := c.Children(0)
	if !ok {
		t.Fatal("Children(0) not ok")
	}

	want := []string{"A-Folder", "z-folder", "a.txt", "b.txt"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Filename != want[i] {
			t.Fatalf("order = %v, want %v", names(got), want)
		}
	}

	// 返回的是副本，修改它不应影响缓存。
	got[0].Filename = "hacked"
	if again, _ := c.Children(0); again[0].Filename != "A-Folder" {
		t.Fatal("Children must return a copy")
	}
}

func TestSaveAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tree.json")

	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c.SetFolder(0, []api.FileInfo{{FileID: 10, Filename: "root-dir", Type: 1}})
	c.SetFolder(10, []api.FileInfo{{FileID: 11, Filename: "file.bin", Size: 1024}})
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	folders, items := c2.Stats()
	if folders != 2 || items != 2 {
		t.Fatalf("stats = %d/%d, want 2/2", folders, items)
	}

	children, ok := c2.Children(10)
	if !ok || len(children) != 1 || children[0].FileID != 11 {
		t.Fatalf("children = %+v", children)
	}

	if _, ok := c2.SyncedAt(0); !ok {
		t.Fatal("SyncedAt(0) not ok")
	}
	if c2.LastSyncedAt().IsZero() {
		t.Fatal("LastSyncedAt is zero")
	}
	if time.Since(c2.LastSyncedAt()) > time.Minute {
		t.Fatal("LastSyncedAt too old")
	}
}

func TestOpenCorruptFileRebuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tree.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Open(path)
	if err == nil {
		t.Fatal("expected error for corrupt cache")
	}
	if folders, _ := c.Stats(); folders != 0 {
		t.Fatalf("folders = %d, want 0", folders)
	}
	if _, statErr := os.Stat(path + ".bad"); statErr != nil {
		t.Fatalf(".bad backup missing: %v", statErr)
	}
}

func names(files []api.FileInfo) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Filename
	}
	return out
}
