package ui

import (
	"testing"

	"pan123/internal/store"
)

func TestSearchModelValue(t *testing.T) {
	m := &searchModel{}
	m.SetHits([]store.SearchHit{
		{Entry: store.Entry{Name: "dir", IsDir: true}, Parent: "/", Path: "/dir"},
		{Entry: store.Entry{Name: "a.bin", Size: 2048}, Parent: "/dir", Path: "/dir/a.bin"},
	})

	if got := m.RowCount(); got != 2 {
		t.Fatalf("RowCount = %d", got)
	}

	cases := []struct {
		row, col int
		want     interface{}
	}{
		{0, 0, "dir"},
		{0, 1, "文件夹"},
		{0, 2, "-"},
		{0, 3, "我的云盘"},
		{1, 0, "a.bin"},
		{1, 1, "文件"},
		{1, 2, "2.0 KB"},
		{1, 3, "我的云盘/dir"},
	}
	for _, c := range cases {
		if got := m.Value(c.row, c.col); got != c.want {
			t.Fatalf("Value(%d,%d) = %v, want %v", c.row, c.col, got, c.want)
		}
	}

	if got := m.Value(5, 0); got != nil {
		t.Fatalf("out-of-range Value = %v", got)
	}
	if hit, ok := m.Hit(1); !ok || hit.Name != "a.bin" {
		t.Fatalf("Hit(1) = %+v, %v", hit, ok)
	}
	if _, ok := m.Hit(-1); ok {
		t.Fatal("Hit(-1) should be false")
	}
	if got := displayDir("/"); got != "我的云盘" {
		t.Fatalf("displayDir(/) = %q", got)
	}
	if got := displayDir("电影/2024"); got != "我的云盘/电影/2024" {
		t.Fatalf("displayDir = %q", got)
	}
}
