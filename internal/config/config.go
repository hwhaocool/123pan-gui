// Package config 负责配置文件与本地缓存目录。
//
// config.json 和 tree.db 都保存在「程序当前工作目录」（通常就是 exe 所在目录），
// 整个程序目录可以直接拷贝带走，方便个人使用。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultQPS 是默认限速（每秒请求数）。WebDAV 一般比开放平台宽松，保守取 5。
	DefaultQPS = 5.0

	// DefaultWebDAVURL 是 123 云盘官方 WebDAV 地址。
	DefaultWebDAVURL = "https://webdav.123pan.cn/webdav"

	// ConfigFileName / CacheFileName 是固定文件名。
	ConfigFileName = "config.json"
	CacheFileName  = "tree.db"
)

// Config 是持久化到 config.json 的配置。
type Config struct {
	// WebDAVURL 是 WebDAV 根地址，例如 https://dav.example.com/dav/123pan
	WebDAVURL string `json:"webdavUrl,omitempty"`
	// Username / Password 用于 Basic Auth（留空表示匿名）。
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// InsecureTLS 跳过 TLS 证书校验（自建/内网 WebDAV 常用）。
	InsecureTLS bool `json:"insecureTls,omitempty"`

	QPS float64 `json:"qps"`

	LastSyncAt time.Time `json:"lastSyncAt,omitzero"`
}

// Default 返回一份默认配置（默认指向 123 云盘官方 WebDAV）。
func Default() *Config {
	return &Config{QPS: DefaultQPS, WebDAVURL: DefaultWebDAVURL}
}

// Sanitize 修正越界/空值。
func (c *Config) Sanitize() {
	if c.QPS <= 0 {
		c.QPS = DefaultQPS
	}
	if c.QPS > 50 {
		c.QPS = 50
	}

	c.WebDAVURL = strings.TrimSpace(c.WebDAVURL)
	if c.WebDAVURL != "" {
		if !strings.Contains(c.WebDAVURL, "://") {
			c.WebDAVURL = "https://" + c.WebDAVURL
		}
		c.WebDAVURL = strings.TrimRight(c.WebDAVURL, "/")
	}
}

// WorkDir 返回 config.json / tree.db 所在目录：程序当前工作目录。
// 若工作目录不可用（例如被删除），回退到可执行文件所在目录。
func WorkDir() (string, error) {
	if wd, err := os.Getwd(); err == nil && wd != "" {
		return wd, nil
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe), nil
	}
	return "", errors.New("无法确定程序工作目录")
}

// ConfigPath 返回配置文件路径（当前工作目录下的 config.json）。
func ConfigPath() (string, error) {
	d, err := WorkDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, ConfigFileName), nil
}

// CachePath 返回 SQLite 缓存文件路径（当前工作目录下的 tree.db）。
func CachePath() (string, error) {
	d, err := WorkDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, CacheFileName), nil
}

// Load 读取配置；文件不存在时返回默认配置（不报错）。
func Load() (cfg *Config, path string, err error) {
	path, err = ConfigPath()
	if err != nil {
		return nil, "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), path, nil
		}
		return nil, path, err
	}

	cfg = Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return Default(), path, fmt.Errorf("配置文件格式错误（已使用默认配置）: %w", err)
	}
	cfg.Sanitize()
	return cfg, path, nil
}

// Save 以原子方式写入配置文件。
func (c *Config) Save(path string) error {
	c.Sanitize()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
