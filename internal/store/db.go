// Package store 用 SQLite 实现本地目录树缓存。
//
// 数据库文件（tree.db）默认放在程序当前工作目录，和 config.json 一起，
// 整个程序目录可以直接拷贝带走。
//
// 缓存以「父目录路径 -> 该目录的直接子项」为粒度保存：
//   - 只同步某一棵子树时，不会破坏其它目录的缓存；
//   - 展开某个节点时可以直接离线读取它的子项，不需要访问 WebDAV；
//   - 需要刷新时按目录整段替换，天然完成增 / 删 / 改的合并。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，无需 CGO
)

// Entry 是缓存中的一个文件或文件夹。
type Entry struct {
	Name        string
	IsDir       bool
	Size        int64
	ETag        string
	Modified    time.Time
	ContentType string
}

const schema = `
CREATE TABLE IF NOT EXISTS folder (
	path      TEXT PRIMARY KEY,
	synced_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS entry (
	parent       TEXT NOT NULL,
	name         TEXT NOT NULL,
	sort_name    TEXT NOT NULL,
	is_dir       INTEGER NOT NULL,
	size         INTEGER NOT NULL,
	etag         TEXT NOT NULL DEFAULT '',
	modified     INTEGER NOT NULL DEFAULT 0,
	content_type TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (parent, name)
);

CREATE INDEX IF NOT EXISTS idx_entry_parent ON entry(parent);
`

// Cache 是 SQLite 目录树缓存，可安全地被多个 goroutine 使用。
type Cache struct {
	db   *sql.DB
	path string
	mu   sync.Mutex // 串行化写事务
}

// Open 打开（必要时创建）SQLite 数据库。
func Open(path string) (*Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// 使用 DELETE 日志模式：不产生常驻的 -wal / -shm 文件，数据库目录更干净。
	// （单连接场景下 WAL 的并发优势用不上，写入提交后日志文件即刻删除。）
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 单一连接：避免 SQLite 并发写锁冲突（UI 读取 + 同步 worker 写入足够快）。
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库失败: %w", err)
	}

	// 老版本用的是 WAL 模式：这里兜底切回 DELETE，并清理可能残留的 -wal / -shm。
	if err := ensureDeleteJournal(db); err != nil {
		db.Close()
		return nil, err
	}
	cleanupSidecarFiles(path)

	return &Cache{db: db, path: path}, nil
}

// ensureDeleteJournal 确保数据库使用 DELETE 日志模式。
func ensureDeleteJournal(db *sql.DB) error {
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		return fmt.Errorf("读取日志模式失败: %w", err)
	}
	if strings.EqualFold(mode, "delete") {
		return nil
	}
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		return fmt.Errorf("切换数据库日志模式失败: %w", err)
	}
	return nil
}

// cleanupSidecarFiles 删除 WAL 模式遗留的 -wal / -shm 文件（尽力而为）。
// 删除失败（例如仍被占用）不影响使用，下次启动会再试。
func cleanupSidecarFiles(path string) {
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			// 忽略：可能是旧的共享内存映射尚未释放，下次启动会再清理。
			_ = err
		}
	}
}

// Path 返回数据库文件路径。
func (c *Cache) Path() string { return c.path }

// Close 关闭数据库。
func (c *Cache) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// Flush 在同步过程中被周期性调用：DELETE 模式下每次写入已提交到主文件，
// 这里只做一次轻量的 PRAGMA optimize（不会产生额外文件）。
func (c *Cache) Flush() error {
	if c == nil || c.db == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.db.Exec(`PRAGMA optimize`)
	return err
}

// Children 返回某目录缓存的子项（文件夹在前，再按名称不分大小写升序）。
// 第二个返回值为 false 表示该目录从未同步过。
func (c *Cache) Children(parent string) ([]Entry, bool) {
	parent = NormalizePath(parent)

	var synced int
	if err := c.db.QueryRow(`SELECT COUNT(1) FROM folder WHERE path = ?`, parent).Scan(&synced); err != nil {
		return nil, false
	}

	rows, err := c.db.Query(`
		SELECT name, is_dir, size, etag, modified, content_type
		FROM entry
		WHERE parent = ?
		ORDER BY is_dir DESC, sort_name ASC, name ASC`, parent)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	entries := make([]Entry, 0, 16)
	for rows.Next() {
		var (
			e        Entry
			isDir    int
			modified int64
		)
		if err := rows.Scan(&e.Name, &isDir, &e.Size, &e.ETag, &modified, &e.ContentType); err != nil {
			return nil, false
		}
		e.IsDir = isDir != 0
		if modified > 0 {
			e.Modified = time.Unix(0, modified)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false
	}
	return entries, synced > 0
}

// SyncedAt 返回某目录上次同步时间。
func (c *Cache) SyncedAt(path string) (time.Time, bool) {
	path = NormalizePath(path)

	var ts int64
	err := c.db.QueryRow(`SELECT synced_at FROM folder WHERE path = ?`, path).Scan(&ts)
	if err != nil || ts <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, ts), true
}

// SetFolder 用最新拉取到的列表整体替换某目录的缓存（单个事务）。
func (c *Cache) SetFolder(parent string, entries []Entry) error {
	parent = NormalizePath(parent)

	// 按名称去重（WebDAV 理论上不会重复，但防御一下）。
	uniq := make(map[string]Entry, len(entries))
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		e.Name = name
		uniq[name] = e
	}

	seen := make(map[string]struct{}, len(uniq))
	list := make([]Entry, 0, len(uniq))
	for _, e := range uniq {
		if _, ok := seen[e.Name]; ok {
			continue
		}
		seen[e.Name] = struct{}{}
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})

	c.mu.Lock()
	defer c.mu.Unlock()

	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM entry WHERE parent = ?`, parent); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO entry(parent, name, sort_name, is_dir, size, etag, modified, content_type)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range list {
		var modified int64
		if !e.Modified.IsZero() {
			modified = e.Modified.UnixNano()
		}
		if _, err := stmt.Exec(parent, e.Name, strings.ToLower(e.Name),
			boolInt(e.IsDir), e.Size, e.ETag, modified, e.ContentType); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`INSERT INTO folder(path, synced_at) VALUES(?, ?)
		ON CONFLICT(path) DO UPDATE SET synced_at = excluded.synced_at`,
		parent, time.Now().UnixNano()); err != nil {
		return err
	}

	return tx.Commit()
}

// Remove 删除某目录及其子目录的缓存（用于强制重新拉取）。
func (c *Cache) Remove(dir string) error {
	dir = NormalizePath(dir)

	c.mu.Lock()
	defer c.mu.Unlock()

	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM entry WHERE parent = ?`, dir); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM folder WHERE path = ?`, dir); err != nil {
		return err
	}
	return tx.Commit()
}

// Stats 返回已缓存目录数与条目总数。
func (c *Cache) Stats() (folders, items int) {
	if err := c.db.QueryRow(`SELECT COUNT(1) FROM folder`).Scan(&folders); err != nil {
		folders = 0
	}
	if err := c.db.QueryRow(`SELECT COUNT(1) FROM entry`).Scan(&items); err != nil {
		items = 0
	}
	return folders, items
}

// LastSyncedAt 返回全部缓存中最新的同步时间。
func (c *Cache) LastSyncedAt() time.Time {
	var ts int64
	if err := c.db.QueryRow(`SELECT COALESCE(MAX(synced_at), 0) FROM folder`).Scan(&ts); err != nil || ts <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ts)
}

// NormalizePath 规范化 WebDAV 绝对路径：以 "/" 开头、去掉多余的斜杠和结尾斜杠。
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "/"
	}
	return p
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// SearchHit 是本地缓存中的一条搜索结果。
type SearchHit struct {
	Entry
	Path   string // 完整路径，例如 "/电影/2024/a.mp4"
	Parent string // 父目录路径，根目录为 "/"
}

// Search 只在本地 SQLite 缓存里按名称做模糊搜索：
//   - 忽略大小写（基于 sort_name，Go 的 Unicode 小写）；
//   - 以空格分隔的多个关键字需要同时命中（AND）；
//   - 结果按「完全匹配 > 前缀匹配 > 包含」排序，其次名称更短/文件夹优先；
//   - 不会访问网络。
func (c *Cache) Search(query string, limit int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}

	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, nil
	}

	conds := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms)+1)
	for _, term := range terms {
		conds = append(conds, `sort_name LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(term)+"%")
	}

	// 多取一些候选，再在 Go 里按相关度排序、截断。
	q := `SELECT parent, name, is_dir, size, etag, modified, content_type
		FROM entry
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY sort_name ASC, parent ASC
		LIMIT ?`
	args = append(args, limit*4)

	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := make([]SearchHit, 0, limit)
	for rows.Next() {
		var (
			h        SearchHit
			isDir    int
			modified int64
		)
		if err := rows.Scan(&h.Parent, &h.Name, &isDir, &h.Size, &h.ETag, &modified, &h.ContentType); err != nil {
			return nil, err
		}
		h.IsDir = isDir != 0
		if modified > 0 {
			h.Modified = time.Unix(0, modified)
		}
		h.Parent = NormalizePath(h.Parent)
		h.Path = entryPath(h.Parent, h.Name)
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	q = strings.ToLower(query)
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]

		if ra, rb := matchRank(a.Name, q), matchRank(b.Name, q); ra != rb {
			return ra < rb
		}
		if len(a.Name) != len(b.Name) {
			return len(a.Name) < len(b.Name)
		}
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if an != bn {
			return an < bn
		}
		return a.Path < b.Path
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// matchRank 给搜索结果打相关度：0 完全相同，1 前缀匹配，2 包含，3 其它（多关键字）。
func matchRank(name, query string) int {
	n := strings.ToLower(name)
	switch {
	case n == query:
		return 0
	case strings.HasPrefix(n, query):
		return 1
	case strings.Contains(n, query):
		return 2
	default:
		return 3
	}
}

// escapeLike 转义 LIKE 通配符，避免用户输入的 % / _ 被当成通配符。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func entryPath(parent, name string) string {
	if parent == "/" || parent == "" {
		return "/" + name
	}
	return parent + "/" + name
}
