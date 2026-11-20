package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"pan123/internal/config"
	"pan123/internal/webdav"
)

// TestToolbarSyncButtons 验证工具栏的同步按钮：只有「和子目录」/「单层」两个，
// 不再有「同步全部」和「包含子目录」复选框。
func TestToolbarSyncButtons(t *testing.T) {
	a := newDuplicateTestApp(t)

	tt := ui.NewTester(a.View, 1040, 400)
	tt.Frame()

	texts := strings.Join(tt.Texts(), "|")
	for _, want := range []string{"同步选中目录(和子目录)", "同步选中目录(单层)"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("toolbar missing %q: %s", want, texts)
		}
	}
	for _, gone := range []string{"同步全部", "包含子目录"} {
		if strings.Contains(texts, gone) {
			t.Fatalf("toolbar should not show %q: %s", gone, texts)
		}
	}
}

// TestSyncSelectedRecursiveFlag 验证两个按钮分别提交「含子目录」/「仅本层」任务，
// 且未选中节点时回退到根目录。
func TestSyncSelectedRecursiveFlag(t *testing.T) {
	a := newDuplicateTestApp(t)
	a.cfg = &config.Config{WebDAVURL: "http://example.invalid/webdav", QPS: 5}
	a.client = webdav.New(a.clientOptions())
	a.syncer = NewSyncer(a.client, a.cache) // 不启动 worker：只进队列，不访问网络
	a.root = NewRootNode()
	a.selected = nil

	a.syncSelected(true)
	a.syncSelected(false)

	logs := strings.Join(a.logs, "\n")
	if !strings.Contains(logs, "已加入同步队列：我的云盘（含子目录）") {
		t.Fatalf("recursive sync log missing: %s", logs)
	}
	if !strings.Contains(logs, "已加入同步队列：我的云盘（仅本层）") {
		t.Fatalf("single-layer sync log missing: %s", logs)
	}

	a.syncer.mu.Lock()
	queue := append([]*Job(nil), a.syncer.queue...)
	a.syncer.mu.Unlock()
	if len(queue) != 2 || !queue[0].Recursive || queue[1].Recursive {
		t.Fatalf("queued jobs = %+v", queue)
	}
	if queue[0].Node != a.root || queue[0].Node.Path() != "/" {
		t.Fatalf("recursive job node = %+v", queue[0].Node)
	}
}
