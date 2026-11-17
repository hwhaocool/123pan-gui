// Package config 负责配置文件与本地缓存目录。
//
// config.json 和 tree.json 都保存在「程序当前工作目录」（通常就是 exe 所在目录），
// 整个程序目录可以直接拷贝带走，方便个人使用。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultBaseURL 是 123 云盘开放平台地址。
	DefaultBaseURL = "https://open-api.123pan.com"
	// DefaultQPS 是默认限速（每秒请求数）。开放平台对单账号有 QPS 限制，保守取 1。
	DefaultQPS = 1.0
	// DefaultPageSize 是文件列表接口的每页条数上限（接口最大 100）。
	DefaultPageSize = 100

	// ConfigFileName / CacheFileName 是固定文件名。
	ConfigFileName = "config.json"
	CacheFileName  = "tree.json"
)

// Config 是持久化到 config.json 的配置。
type Config struct {
	// AccessToken 是 openapi 访问凭证；可由 clientID/clientSecret 自动获取并回写到本地。
	AccessToken    string    `json:"accessToken,omitempty"`
	TokenExpiredAt time.Time `json:"tokenExpiredAt,omitzero"`
	// ClientID/ClientSecret 用于自动换取 access_token。
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`

	QPS      float64 `json:"qps"`
	PageSize int     `json:"pageSize"`
	BaseURL  string  `json:"baseUrl"`

	LastSyncAt time.Time `json:"lastSyncAt,omitzero"`
}

// Default 返回一份默认配置。
func Default() *Config {
	return &Config{
		QPS:      DefaultQPS,
		PageSize: DefaultPageSize,
		BaseURL:  DefaultBaseURL,
	}
}

// Sanitize 修正越界/空值。
func (c *Config) Sanitize() {
	if c.QPS <= 0 {
		c.QPS = DefaultQPS
	}
	if c.QPS > 20 {
		c.QPS = 20
	}
	if c.PageSize <= 0 || c.PageSize > 100 {
		c.PageSize = DefaultPageSize
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
}

// WorkDir 返回 config.json 所在目录：程序当前工作目录。
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

// CachePath 返回目录树缓存文件路径（当前工作目录下的 tree.json）。
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
