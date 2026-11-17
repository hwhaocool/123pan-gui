package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPathsInWorkDir 验证 config.json / tree.db 都落在程序当前工作目录。
func TestPathsInWorkDir(t *testing.T) {
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
		t.Fatalf("config path = %q, want under %q", path, dir)
	}

	cachePath, err := CachePath()
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if filepath.Base(cachePath) != CacheFileName || filepath.Ext(cachePath) != ".db" {
		t.Fatalf("cache file name = %q", filepath.Base(cachePath))
	}
	if !strings.EqualFold(filepath.Clean(cachePath), filepath.Clean(filepath.Join(dir, CacheFileName))) {
		t.Fatalf("cache path = %q, want under %q", cachePath, dir)
	}
}

// TestLoadDefaultsAndRoundTrip 验证默认配置与保存/读取。
func TestLoadDefaultsAndRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, path, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.QPS != DefaultQPS || cfg.WebDAVURL != DefaultWebDAVURL {
		t.Fatalf("defaults = %+v", cfg)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config file should not exist yet: %v", err)
	}

	cfg.WebDAVURL = "https://dav.example.com/dav/123pan"
	cfg.Username = "user"
	cfg.Password = "p@ss"
	cfg.InsecureTLS = true
	cfg.QPS = 8
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
	if loaded.WebDAVURL != cfg.WebDAVURL || loaded.Username != "user" || loaded.Password != "p@ss" {
		t.Fatalf("credentials = %+v", loaded)
	}
	if !loaded.InsecureTLS || loaded.QPS != 8 {
		t.Fatalf("loaded = %+v", loaded)
	}
}

// TestSanitize 验证越界值/URL 规范化。
func TestSanitize(t *testing.T) {
	c := &Config{QPS: 0, WebDAVURL: "  dav.example.com/dav/123/  "}
	c.Sanitize()
	if c.QPS != DefaultQPS {
		t.Fatalf("qps = %v", c.QPS)
	}
	if c.WebDAVURL != "https://dav.example.com/dav/123" {
		t.Fatalf("url = %q", c.WebDAVURL)
	}

	c = &Config{QPS: 999, WebDAVURL: "http://192.168.1.10:8080/dav/"}
	c.Sanitize()
	if c.QPS != 50 {
		t.Fatalf("qps = %v", c.QPS)
	}
	if c.WebDAVURL != "http://192.168.1.10:8080/dav" {
		t.Fatalf("url = %q", c.WebDAVURL)
	}

	c = &Config{QPS: -1}
	c.Sanitize()
	if c.QPS != DefaultQPS {
		t.Fatalf("qps = %v", c.QPS)
	}
}

// TestDefaultWebDAVURL 验证默认配置指向 123 云盘官方 WebDAV。
func TestDefaultWebDAVURL(t *testing.T) {
	c := Default()
	if c.WebDAVURL != "https://webdav.123pan.cn/webdav" {
		t.Fatalf("default webdav url = %q", c.WebDAVURL)
	}
}
