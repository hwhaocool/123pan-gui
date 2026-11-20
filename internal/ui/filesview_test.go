package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"pan123/internal/store"
)

// TestFilesViewShowsETag 验证文件列表包含 ETag 列并展示条目的 ETag。
func TestFilesViewShowsETag(t *testing.T) {
	a := newDuplicateTestApp(t)
	a.root = NewRootNode()
	a.applyEntries(a.root, []store.Entry{
		{Name: "a.jpg", Size: 1024, ETag: "abc123"},
		{Name: "docs", IsDir: true},
	})

	tt := ui.NewTester(a.View, 1200, 500)
	tt.Frame()

	texts := strings.Join(tt.Texts(), "|")
	if !strings.Contains(texts, "ETag") {
		t.Fatalf("files view missing ETag column: %s", texts)
	}
	if !strings.Contains(texts, "abc123") {
		t.Fatalf("files view missing ETag value: %s", texts)
	}
}
