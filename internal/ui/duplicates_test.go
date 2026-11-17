package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"pan123/internal/store"
)

// newDuplicateTestApp 构造一个只依赖本地缓存的最小 App（不启动同步 worker / 窗口）。
func newDuplicateTestApp(t *testing.T) *App {
	t.Helper()

	cache, err := store.Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { cache.Close() })

	if err := cache.SetFolder("/", []store.Entry{
		{Name: "A.TXT", Size: 10},
		{Name: "docs", IsDir: true},
	}); err != nil {
		t.Fatalf("SetFolder /: %v", err)
	}
	if err := cache.SetFolder("/docs", []store.Entry{
		{Name: "a.txt", Size: 20},
		{Name: "only.txt", Size: 30},
	}); err != nil {
		t.Fatalf("SetFolder /docs: %v", err)
	}

	return &App{
		cache:           cache,
		status:          "就绪",
		topPercent:      74,
		leftWidth:       320,
		fileSelected:    -1,
		selectedRow:     -1,
		searchSelected:  -1,
		dupSelected:     -1,
		dupFileSelected: -1,
		dupShownGroup:   -1,
		dupWidth:        260,
	}
}

func TestDetectDuplicatesBuildsGroups(t *testing.T) {
	a := newDuplicateTestApp(t)

	a.detectDuplicates()

	if !a.showDupes {
		t.Fatal("detectDuplicates should open the duplicates view")
	}
	if len(a.dupGroups) != 1 {
		t.Fatalf("groups = %+v, want 1 group", a.dupGroups)
	}
	g := a.dupGroups[0]
	if len(g.Files) != 2 {
		t.Fatalf("files = %+v, want 2", g.Files)
	}
	if a.dupSelected != 0 {
		t.Fatalf("dupSelected = %d, want 0", a.dupSelected)
	}
	if a.dupShownGroup != -1 {
		t.Fatalf("dupShownGroup = %d, want -1 (reset on detect)", a.dupShownGroup)
	}
	if len(a.logs) == 0 || !strings.Contains(strings.Join(a.logs, "\n"), "1 组同名文件") {
		t.Fatalf("logs = %v", a.logs)
	}
}

func TestDuplicatesViewRenders(t *testing.T) {
	a := newDuplicateTestApp(t)
	a.detectDuplicates()

	tt := ui.NewTester(a.View, 1040, 760)
	tt.Frame()

	texts := strings.Join(tt.Texts(), "|")
	for _, want := range []string{"重复文件检测", "a.txt", "我的云盘", "个同名文件"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("rendered texts missing %q: %s", want, texts)
		}
	}
	if !strings.Contains(texts, "A.TXT") {
		t.Fatalf("right list should show the file name: %s", texts)
	}
}
