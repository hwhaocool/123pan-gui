// Package store 实现本地目录树缓存。
//
// 缓存以「父目录 ID -> 该目录下的直接子项」为单位保存，这样：
//   - 只同步某一棵子树时，不会破坏其它目录的缓存；
//   - 界面展开某个节点时可以直接离线读取它的子项，不需要请求接口；
//   - 需要刷新时按目录整段替换，天然完成了增/删/改的合并。
package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"pan123/internal/api"
)

// cacheVersion 是缓存文件格式版本。
const cacheVersion = 1

// Folder 是一个目录的缓存内容。
type Folder struct {
	Children []api.FileInfo `json:"children"`
	SyncedAt time.Time      `json:"syncedAt"`
}

// Cache 是线程安全的本地缓存。
type Cache struct {
	Version int               `json:"version"`
	Root    int64             `json:"root"`
	Folders map[int64]*Folder `json:"folders"`

	path string
	mu   sync.RWMutex
}

// Open 加载缓存文件。文件不存在时返回空缓存；文件损坏时会备份后重建。
func Open(path string) (*Cache, error) {
	c := &Cache{
		Version: cacheVersion,
		Folders: make(map[int64]*Folder),
		path:    path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}

	if err := json.Unmarshal(data, c); err != nil {
		backup := path + ".bad"
		if renameErr := os.Rename(path, backup); renameErr != nil {
			log.Printf("缓存文件损坏，且备份失败: %v", renameErr)
		} else {
			log.Printf("缓存文件损坏，已备份为 %s: %v", backup, err)
		}
		c.Version = cacheVersion
		c.Folders = make(map[int64]*Folder)
		return c, fmt.Errorf("缓存文件损坏，已重建: %w", err)
	}

	if c.Folders == nil {
		c.Folders = make(map[int64]*Folder)
	}
	if c.Version == 0 {
		c.Version = cacheVersion
	}
	return c, nil
}

// Path 返回缓存文件路径。
func (c *Cache) Path() string { return c.path }

// Children 返回某目录缓存的子项副本。第二个返回值为 false 表示该目录从未同步过。
func (c *Cache) Children(parent int64) ([]api.FileInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	f, ok := c.Folders[parent]
	if !ok {
		return nil, false
	}
	out := make([]api.FileInfo, len(f.Children))
	copy(out, f.Children)
	return out, true
}

// SyncedAt 返回某目录上次同步时间。
func (c *Cache) SyncedAt(parent int64) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	f, ok := c.Folders[parent]
	if !ok {
		return time.Time{}, false
	}
	return f.SyncedAt, true
}

// SetFolder 用最新拉取到的列表整体替换某目录的缓存。
// 回收站条目会被过滤，顺序为「文件夹在前，然后按名称不分大小写升序」。
func (c *Cache) SetFolder(parent int64, files []api.FileInfo) int {
	kept := make([]api.FileInfo, 0, len(files))
	skipped := 0
	for _, f := range files {
		if f.Trashed != 0 {
			skipped++
			continue
		}
		kept = append(kept, f)
	}
	sortFiles(kept)

	c.mu.Lock()
	c.Folders[parent] = &Folder{Children: kept, SyncedAt: time.Now()}
	c.mu.Unlock()

	return skipped
}

// Remove 删除某目录的缓存（用于强制重新拉取）。
func (c *Cache) Remove(parent int64) {
	c.mu.Lock()
	delete(c.Folders, parent)
	c.mu.Unlock()
}

// Stats 返回已缓存目录数与条目总数。
func (c *Cache) Stats() (folders, items int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, f := range c.Folders {
		items += len(f.Children)
	}
	return len(c.Folders), items
}

// LastSyncedAt 返回全部缓存中最新的同步时间。
func (c *Cache) LastSyncedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var latest time.Time
	for _, f := range c.Folders {
		if f.SyncedAt.After(latest) {
			latest = f.SyncedAt
		}
	}
	return latest
}

// Save 以原子方式写回磁盘。
func (c *Cache) Save() error {
	payload := struct {
		Version int               `json:"version"`
		Folders map[int64]*Folder `json:"folders"`
	}{Version: cacheVersion}

	c.mu.RLock()
	payload.Folders = c.Folders
	data, err := json.MarshalIndent(payload, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func sortFiles(files []api.FileInfo) {
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if a.IsDir() != b.IsDir() {
			return a.IsDir()
		}
		an, bn := strings.ToLower(a.Filename), strings.ToLower(b.Filename)
		if an != bn {
			return an < bn
		}
		return a.FileID < b.FileID
	})
}
