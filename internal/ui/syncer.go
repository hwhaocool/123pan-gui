package ui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"pan123/internal/api"
	"pan123/internal/store"
)

// Job 是一次同步请求。
type Job struct {
	Node      *Node
	Recursive bool
	// Reason 仅用于日志，例如 "expand"（展开时自动同步）。
	Reason string
}

// Syncer 用单个 goroutine 串行执行同步任务。
// 所有网络请求都经过 api.Client 内部的限速器，因此多任务排队也不会突破 QPS 限制。
type Syncer struct {
	client *api.Client
	cache  *store.Cache

	// 由 App 注入。
	synchronize func(func())                     // 投递到 UI 线程（异步）
	apply       func(*Node, []api.FileInfo)      // 在 UI 线程更新树模型
	logf        func(format string, args ...any) // 写日志
	status      func(string)                     // 更新状态栏
	busy        func(bool)                       // 更新按钮可用状态
	done        func(*Node)                      // 一个任务结束时回调（用于展开节点）

	mu      sync.Mutex
	queue   []*Job
	signal  chan struct{}
	quit    chan struct{}
	closed  bool
	cancel  context.CancelFunc
	running bool
}

// NewSyncer 创建同步器。
func NewSyncer(client *api.Client, cache *store.Cache) *Syncer {
	s := &Syncer{
		client: client,
		cache:  cache,
		signal: make(chan struct{}, 1),
		quit:   make(chan struct{}),
	}
	return s
}

// Start 启动后台 worker。
func (s *Syncer) Start() {
	go s.run()
}

// Busy 表示是否正在同步。
func (s *Syncer) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Submit 把任务放入队列（非阻塞）。相同节点 + 相同递归标志的任务会被去重。
func (s *Syncer) Submit(j *Job) error {
	if j == nil || j.Node == nil {
		return errors.New("无效的同步任务")
	}
	if !j.Node.IsDir() {
		return errors.New("只能同步文件夹")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("程序正在退出")
	}
	if len(s.queue) >= 64 {
		s.mu.Unlock()
		return errors.New("同步队列已满，请稍后再试")
	}
	for _, q := range s.queue {
		if q.Node == j.Node && q.Recursive == j.Recursive {
			s.mu.Unlock()
			return errors.New("该目录已在同步队列中")
		}
	}
	s.queue = append(s.queue, j)
	s.mu.Unlock()

	select {
	case s.signal <- struct{}{}:
	default:
	}
	return nil
}

// Stop 取消当前任务并清空排队任务。
func (s *Syncer) Stop() {
	s.mu.Lock()
	queued := len(s.queue)
	s.queue = nil
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if queued > 0 {
		s.logf("已清空 %d 个排队任务", queued)
	}
}

// Close 停止 worker（退出程序时调用）。
func (s *Syncer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancel := s.cancel
	close(s.quit)
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (s *Syncer) run() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.mu.Unlock()
			select {
			case <-s.signal:
			case <-s.quit:
			}
			s.mu.Lock()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		job := s.queue[0]
		s.queue = s.queue[1:]
		s.running = true
		s.mu.Unlock()

		s.busy(true)
		s.process(job)

		s.mu.Lock()
		s.running = false
		hasMore := len(s.queue) > 0 && !s.closed
		s.mu.Unlock()
		if !hasMore {
			s.busy(false)
		}
	}
}

func (s *Syncer) process(job *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	node := job.Node
	recursive := job.Recursive
	start := time.Now()

	if s.done != nil {
		defer s.done(node)
	}

	s.logf("开始同步 %s（%s，QPS=%.1f，请求间隔≈%s）", node.Path(), onOff(recursive, "含子目录", "仅本层"), s.client.QPS(), s.client.Interval())
	s.status("准备同步…")

	stack := []*Node{node}
	folders, items, total := 0, 0, 0

	for len(stack) > 0 {
		if ctx.Err() != nil {
			s.logf("已停止同步 %s（完成 %d 个目录 / %d 项，用时 %s）", node.Path(), folders, items, time.Since(start).Round(time.Second))
			s.status("已停止")
			return
		}

		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !cur.IsDir() {
			continue
		}

		s.status(fmt.Sprintf("同步中：%s", cur.Path()))
		files, err := s.client.ListAll(ctx, cur.FileID())
		if err != nil {
			if ctx.Err() != nil {
				s.logf("已停止同步 %s（完成 %d 个目录 / %d 项）", node.Path(), folders, items)
				s.status("已停止")
				return
			}
			s.logf("同步失败：%s：%v", cur.Path(), err)
			if errors.Is(err, api.ErrNotConfigured) || api.IsAuthError(err) {
				s.status("同步失败")
				s.saveCache()
				return
			}
			// 单个目录失败（例如刚被删除）不影响其它目录继续同步。
			continue
		}

		skipped := s.cache.SetFolder(cur.FileID(), files)
		cached, _ := s.cache.Children(cur.FileID())

		// 在 UI 线程同步更新模型，保证下面 ChildDirs() 读到的是最新数据。
		s.callUI(func() { s.apply(cur, cached) })

		folders++
		total += len(files)
		items += len(cached)
		s.logf("  ✓ %s（%d 项%s）", cur.Path(), len(cached), trashedHint(skipped))

		if folders%20 == 0 {
			s.saveCache()
		}

		if recursive {
			dirs := cur.ChildDirs()
			for i := len(dirs) - 1; i >= 0; i-- {
				stack = append(stack, dirs[i])
			}
		}
	}

	s.saveCache()
	s.status(fmt.Sprintf("同步完成：%d 个目录 / %d 项", folders, items))
	s.logf("同步完成：%s（请求 %d 个目录，共 %d 项，用时 %s）", node.Path(), folders, total, time.Since(start).Round(time.Second))
}

func (s *Syncer) callUI(f func()) {
	if s.synchronize == nil {
		f()
		return
	}
	done := make(chan struct{})
	s.synchronize(func() {
		defer close(done)
		f()
	})
	<-done
}

func (s *Syncer) saveCache() {
	if err := s.cache.Save(); err != nil {
		s.logf("保存本地缓存失败：%v", err)
	}
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func trashedHint(skipped int) string {
	if skipped <= 0 {
		return ""
	}
	return fmt.Sprintf("，忽略回收站 %d 项", skipped)
}
