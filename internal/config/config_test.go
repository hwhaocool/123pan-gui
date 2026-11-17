package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConfigPathInWorkDir 验证 config.json 落在程序当前工作目录。
func TestConfigPathInWorkDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if filepath.Base(path) != ConfigFileName {
		t.Fatalf("config file name = %q", filepath.Base(path))
	}
	if !strings.EqualFold(filepath.Clean(path), filepath.Clean(filepath.Join(dir, ConfigFileName))) {
		t.Fatalf("config path = %q, want it under %q", path, dir)
	}

	cachePath, err := CachePath()
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(cachePath), filepath.Clean(filepath.Join(dir, CacheFileName))) {
		t.Fatalf("cache path = %q, want it under %q", cachePath, dir)
	}
}

// TestLoadDefaultsAndRoundTrip 验证默认配置与保存/读取。
func TestLoadDefaultsAndRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, path, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.QPS != DefaultQPS || cfg.PageSize != DefaultPageSize || cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config file should not exist yet: %v", err)
	}

	cfg.ClientID = "cid"
	cfg.ClientSecret = "secret"
	cfg.AccessToken = "tok"
	cfg.TokenExpiredAt = time.Now().Add(time.Hour).Truncate(time.Second)
	cfg.QPS = 2.5
	cfg.PageSize = 50
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, loadedPath, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loadedPath != path {
		t.Fatalf("path = %q, want %q", loadedPath, path)
	}
	if loaded.ClientID != "cid" || loaded.ClientSecret != "secret" || loaded.AccessToken != "tok" {
		t.Fatalf("credentials = %+v", loaded)
	}
	if !loaded.TokenExpiredAt.Equal(cfg.TokenExpiredAt) {
		t.Fatalf("expiredAt = %s, want %s", loaded.TokenExpiredAt, cfg.TokenExpiredAt)
	}
	if loaded.QPS != 2.5 || loaded.PageSize != 50 {
		t.Fatalf("qps/pageSize = %v/%d", loaded.QPS, loaded.PageSize)
	}
}

// TestSanitize 验证越界值被修正。
func TestSanitize(t *testing.T) {
	c := &Config{QPS: 0, PageSize: 500, BaseURL: ""}
	c.Sanitize()
	if c.QPS != DefaultQPS || c.PageSize != DefaultPageSize || c.BaseURL != DefaultBaseURL {
		t.Fatalf("sanitized = %+v", c)
	}

	c = &Config{QPS: 100, PageSize: -1, BaseURL: "http://x"}
	c.Sanitize()
	if c.QPS != 20 || c.PageSize != DefaultPageSize || c.BaseURL != "http://x" {
		t.Fatalf("sanitized = %+v", c)
	}
}
