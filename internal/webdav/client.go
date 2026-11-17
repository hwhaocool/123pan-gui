// Package webdav 实现本项目需要的只读 WebDAV 客户端：
//
//   - PROPFIND（Depth 0/1）列目录 / 探测连通性；
//   - Basic Auth、可跳过 TLS 证书校验（自建/内网 WebDAV 常用）；
//   - 全局 QPS 限速（所有请求串行排队）与 429/5xx 退避重试；
//   - 手动处理重定向（301/302/307/308），避免 Go 把 PROPFIND 改成 GET。
package webdav

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"pan123/internal/store"
)

// DefaultQPS 是默认限速。WebDAV 通常比开放平台宽松，但仍保守取 5。
const DefaultQPS = 5.0

// ErrNotConfigured 表示尚未配置 WebDAV 地址。
var ErrNotConfigured = errors.New("尚未配置 WebDAV 地址，请打开「设置…」填写")

// AuthError 表示鉴权失败（401/403）。
type AuthError struct {
	Status int
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("WebDAV 鉴权失败（HTTP %d），请检查用户名 / 密码", e.Status)
}

// HTTPError 是其它非预期状态码。
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	hint := ""
	switch e.Status {
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		hint = "（该地址可能不是 WebDAV 服务）"
	case http.StatusNotFound:
		hint = "（路径不存在）"
	}
	body := strings.TrimSpace(e.Body)
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	if body != "" {
		return fmt.Sprintf("WebDAV 返回 HTTP %d%s: %s", e.Status, hint, body)
	}
	return fmt.Sprintf("WebDAV 返回 HTTP %d%s", e.Status, hint)
}

// IsAuthError 判断是否鉴权错误。
func IsAuthError(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae)
}

// IsNotFound 判断是否 404。
func IsNotFound(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

// Limiter 是匀速限速器：保证两次请求之间至少间隔 interval。
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

// NewLimiter 创建限速器，qps <= 0 时按 1 处理。
func NewLimiter(qps float64) *Limiter {
	l := &Limiter{}
	l.SetQPS(qps)
	return l
}

// SetQPS 调整限速。
func (l *Limiter) SetQPS(qps float64) {
	if qps <= 0 {
		qps = 1
	}
	d := time.Duration(float64(time.Second) / qps)
	if d < time.Millisecond {
		d = time.Millisecond
	}

	l.mu.Lock()
	l.interval = d
	l.mu.Unlock()
}

// Interval 返回两次请求之间的最小间隔。
func (l *Limiter) Interval() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interval
}

// Wait 阻塞直到获得下一个请求时间片，返回实际等待时长。
func (l *Limiter) Wait(ctx context.Context) (time.Duration, error) {
	l.mu.Lock()
	now := time.Now()
	if l.next.Before(now) {
		l.next = now
	}
	slot := l.next
	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	d := time.Until(slot)
	if d <= 0 {
		return 0, ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return d, ctx.Err()
	case <-timer.C:
		return d, nil
	}
}

// Options 是 WebDAV 客户端配置。
type Options struct {
	BaseURL     string // 例如 https://dav.example.com/dav/123pan
	Username    string
	Password    string
	InsecureTLS bool
	QPS         float64
	Timeout     time.Duration
}

// Client 是只读 WebDAV 客户端。
type Client struct {
	limiter *Limiter

	mu       sync.RWMutex
	baseURL  string // 已去掉结尾斜杠
	basePath string // URL 中的路径部分，如 "/dav/123pan"
	username string
	password string
	http     *http.Client

	// OnWait 在因限速等待超过 300ms 时回调（在发起请求的 goroutine 中执行）。
	OnWait func(time.Duration)
}

// New 创建客户端。
func New(o Options) *Client {
	c := &Client{limiter: NewLimiter(o.QPS)}
	c.Configure(o)
	return c
}

// Configure 更新配置；TLS 设置变化时会重建底层 http.Client。
func (c *Client) Configure(o Options) {
	base := strings.TrimSpace(o.BaseURL)
	basePath := ""

	if base != "" {
		if !strings.Contains(base, "://") {
			base = "https://" + base
		}
		if u, err := url.Parse(base); err == nil {
			basePath = strings.TrimSuffix(u.Path, "/")
			u.Path = basePath
			u.RawPath = ""
			base = strings.TrimSuffix(u.String(), "/")
		}
	}

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	if o.InsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式选择忽略证书错误
	}
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// 重定向自己处理：Go 默认会把 PROPFIND 301/302 改成 GET。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	c.mu.Lock()
	c.baseURL = base
	c.basePath = basePath
	c.username = strings.TrimSpace(o.Username)
	c.password = o.Password
	c.http = httpClient
	c.mu.Unlock()

	c.limiter.SetQPS(o.QPS)
}

// Configured 表示是否已填写 WebDAV 地址。
func (c *Client) Configured() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL != ""
}

// BaseURL 返回配置的 WebDAV 地址。
func (c *Client) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL
}

// QPS 返回当前限速值。
func (c *Client) QPS() float64 {
	d := c.limiter.Interval()
	if d <= 0 {
		return 1
	}
	return float64(time.Second) / float64(d)
}

// Interval 返回请求最小间隔。
func (c *Client) Interval() time.Duration { return c.limiter.Interval() }

// SetQPS 调整限速。
func (c *Client) SetQPS(qps float64) { c.limiter.SetQPS(qps) }

// Check 访问根目录（Depth 0）验证地址/凭证是否可用。
func (c *Client) Check(ctx context.Context) error {
	if !c.Configured() {
		return ErrNotConfigured
	}
	target, err := c.urlFor("/", false)
	if err != nil {
		return err
	}
	_, code, err := c.propfind(ctx, target, 0)
	if err != nil {
		return err
	}
	if code != http.StatusMultiStatus && code != http.StatusOK {
		return &HTTPError{Status: code}
	}
	return nil
}

// List 列出 dir 下的直接子项（PROPFIND Depth 1）。dir 为相对 WebDAV 根目录的绝对路径。
func (c *Client) List(ctx context.Context, dir string) ([]store.Entry, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}

	dir = store.NormalizePath(dir)
	target, err := c.urlFor(dir, true)
	if err != nil {
		return nil, err
	}

	data, code, err := c.propfind(ctx, target, 1)
	if err != nil {
		return nil, err
	}
	if code != http.StatusMultiStatus && code != http.StatusOK {
		return nil, &HTTPError{Status: code, Body: snippet(data)}
	}

	return c.parseEntries(dir, data)
}

// Stat 返回单个资源的信息（PROPFIND Depth 0）。
func (c *Client) Stat(ctx context.Context, p string) (store.Entry, error) {
	if !c.Configured() {
		return store.Entry{}, ErrNotConfigured
	}

	p = store.NormalizePath(p)
	target, err := c.urlFor(p, false)
	if err != nil {
		return store.Entry{}, err
	}

	data, code, err := c.propfind(ctx, target, 0)
	if err != nil {
		return store.Entry{}, err
	}
	if code != http.StatusMultiStatus && code != http.StatusOK {
		return store.Entry{}, &HTTPError{Status: code, Body: snippet(data)}
	}

	entries, err := c.parseEntries(parentOf(p), data)
	if err != nil {
		return store.Entry{}, err
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name, path.Base(p)) || p == "/" {
			return e, nil
		}
	}
	if len(entries) > 0 {
		return entries[0], nil
	}
	return store.Entry{}, &HTTPError{Status: http.StatusNotFound}
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:displayname/>
    <d:resourcetype/>
    <d:getcontentlength/>
    <d:getetag/>
    <d:getlastmodified/>
    <d:getcontenttype/>
  </d:prop>
</d:propfind>`

func (c *Client) propfind(ctx context.Context, target string, depth int) ([]byte, int, error) {
	return c.do(ctx, "PROPFIND", target, []byte(propfindBody), strconv.Itoa(depth))
}

// do 执行请求：限速 + 重试 + 手动跟随重定向。
func (c *Client) do(ctx context.Context, method, target string, body []byte, depth string) ([]byte, int, error) {
	const maxAttempts = 4

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(backoff):
			}
		}

		waited, err := c.limiter.Wait(ctx)
		if err != nil {
			return nil, 0, err
		}
		if waited > 300*time.Millisecond && c.OnWait != nil {
			c.OnWait(waited)
		}

		data, status, retryAfter, err := c.roundTrip(ctx, method, target, body, depth)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			// 网络错误（拒绝连接/域名解析失败等）通常不会立刻恢复，只再试一次，
			// 避免离线启动时要等好几秒才提示。
			lastErr = fmt.Errorf("网络请求失败: %w", err)
			if attempt >= 1 {
				break
			}
			continue
		}

		switch {
		case status == http.StatusTooManyRequests:
			lastErr = fmt.Errorf("请求过于频繁（HTTP 429）")
			if retryAfter > 0 {
				select {
				case <-ctx.Done():
					return nil, status, ctx.Err()
				case <-time.After(retryAfter):
				}
			}
			continue
		case status >= 500:
			lastErr = fmt.Errorf("WebDAV 服务端错误（HTTP %d）", status)
			continue
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return data, status, &AuthError{Status: status}
		}

		return data, status, nil
	}
	if lastErr == nil {
		lastErr = errors.New("WebDAV 请求失败")
	}
	return nil, 0, lastErr
}

// roundTrip 发送一次请求（含重定向跟随）。
func (c *Client) roundTrip(ctx context.Context, method, target string, body []byte, depth string) ([]byte, int, time.Duration, error) {
	c.mu.RLock()
	httpClient := c.http
	username, password := c.username, c.password
	c.mu.RUnlock()

	curURL := target
	for hop := 0; hop < 5; hop++ {
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, curURL, rdr)
		if err != nil {
			return nil, 0, 0, err
		}
		if body != nil {
			req.ContentLength = int64(len(body))
		}
		if depth != "" {
			req.Header.Set("Depth", depth)
		}
		req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
		req.Header.Set("Accept", "application/xml, text/xml, */*")
		req.Header.Set("User-Agent", "pan123-client/1.0 (WebDAV)")
		if username != "" || password != "" {
			req.SetBasicAuth(username, password)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, 0, 0, err
		}

		loc := resp.Header.Get("Location")
		if isRedirect(resp.StatusCode) && loc != "" {
			next, err := resolveURL(curURL, loc)
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if err != nil {
				return nil, 0, 0, err
			}
			curURL = next
			continue
		}

		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		if readErr != nil {
			return nil, 0, 0, readErr
		}
		return data, resp.StatusCode, retryAfter, nil
	}
	return nil, 0, 0, errors.New("WebDAV 重定向过多")
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func resolveURL(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// urlFor 把相对路径转换为完整 URL；目录列目录时补结尾斜杠。
func (c *Client) urlFor(p string, collection bool) (string, error) {
	c.mu.RLock()
	base, basePath := c.baseURL, c.basePath
	c.mu.RUnlock()

	if base == "" {
		return "", ErrNotConfigured
	}

	p = store.NormalizePath(p)
	escaped := ""
	if p != "/" {
		segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		escaped = "/" + strings.Join(segs, "/")
	}

	target := base + escaped
	if collection && !strings.HasSuffix(target, "/") {
		target += "/"
	}
	_ = basePath // basePath 仅用于解析 href
	return target, nil
}

// relPath 把 WebDAV 响应里的 href 转成相对 WebDAV 根目录的路径。
func (c *Client) relPath(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}

	p := href
	if u, err := url.Parse(href); err == nil {
		if u.Path != "" {
			p = u.Path // url.Parse 已完成百分号解码
		}
	}

	c.mu.RLock()
	basePath := c.basePath
	c.mu.RUnlock()

	if basePath != "" {
		switch {
		case p == basePath:
			p = "/"
		case strings.HasPrefix(p, basePath+"/"):
			p = p[len(basePath):]
		}
	}

	return store.NormalizePath("/" + strings.TrimPrefix(p, "/"))
}

type multistatus struct {
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href      string        `xml:"href"`
	Propstats []davPropstat `xml:"propstat"`
}

type davPropstat struct {
	Status string  `xml:"status"`
	Prop   davProp `xml:"prop"`
}

type davProp struct {
	DisplayName   string          `xml:"displayname"`
	ContentLength string          `xml:"getcontentlength"`
	ETag          string          `xml:"getetag"`
	LastModified  string          `xml:"getlastmodified"`
	ContentType   string          `xml:"getcontenttype"`
	ResourceType  davResourceType `xml:"resourcetype"`
}

type davResourceType struct {
	Collection *struct{} `xml:"collection"`
}

func (r davResponse) prop() davProp {
	for _, ps := range r.Propstats {
		if strings.Contains(ps.Status, " 200 ") || strings.HasSuffix(strings.TrimSpace(ps.Status), "200") {
			return ps.Prop
		}
	}
	if len(r.Propstats) > 0 {
		return r.Propstats[0].Prop
	}
	return davProp{}
}

func (c *Client) parseEntries(dir string, data []byte) ([]store.Entry, error) {
	var ms multistatus
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, fmt.Errorf("解析 WebDAV 响应失败: %w", err)
	}

	dir = store.NormalizePath(dir)
	entries := make([]store.Entry, 0, len(ms.Responses))

	for _, r := range ms.Responses {
		p := c.relPath(r.Href)
		if p == "" || p == dir {
			continue // 响应里第一个通常是目录自身
		}

		prop := r.prop()

		name := strings.TrimSpace(prop.DisplayName)
		if name == "" {
			name = path.Base(p)
		}
		if name == "" || name == "." || name == "/" {
			continue
		}

		e := store.Entry{
			Name:        name,
			IsDir:       prop.ResourceType.Collection != nil,
			ETag:        strings.Trim(strings.TrimSpace(prop.ETag), `"`),
			ContentType: strings.TrimSpace(prop.ContentType),
			Modified:    parseHTTPTime(prop.LastModified),
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(prop.ContentLength), 10, 64); err == nil && n > 0 {
			e.Size = n
		}

		entries = append(entries, e)
	}

	return entries, nil
}

func parseHTTPTime(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	if t, err := http.ParseTime(v); err == nil {
		return t
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parentOf(p string) string {
	p = store.NormalizePath(p)
	if p == "/" {
		return "/"
	}
	return path.Dir(p)
}

func snippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
