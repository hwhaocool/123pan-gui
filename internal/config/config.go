// Package config 负责配置文件与本地缓存目录。
package config

import (
	"encoding/json"
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
)

// Config 是持久化到 config.json 的配置。
type Config struct {
	AccessToken    string    `json:"accessToken,omitempty"`
	TokenExpiredAt time.Time `json:"tokenExpiredAt,omitempty"`
	ClientID       string    `json:"clientId,omitempty"`
	ClientSecret   string    `json:"clientSecret,omitempty"`

	QPS      float64 `json:"qps"`
	PageSize int     `json:"pageSize"`
	BaseURL  string  `json:"baseUrl"`

	LastSyncAt time.Time `json:"lastSyncAt,omitempty"`
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

// Dir 返回应用数据目录（%APPDATA%\123pan-client）。
func Dir() (string, error) {
	if d := os.Getenv("APPDATA"); d != "" {
		return filepath.Join(d, "123pan-client"), nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "123pan-client"), nil
}

// ConfigPath 返回配置文件路径。
func ConfigPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// CachePath 返回目录树缓存文件路径。
func CachePath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tree.json"), nil
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
