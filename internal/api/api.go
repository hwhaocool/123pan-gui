// Package api 封装 123 云盘开放平台（open-api.123pan.com）的极少数接口，
// 目前只实现了「获取文件列表」和「access_token 换取」，
// 并内置 QPS 限速、429/5xx 退避重试。
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL 是 123 云盘开放平台地址。
const DefaultBaseURL = "https://open-api.123pan.com"

// FileInfo 对应 /api/v2/file/list 返回的 fileList 元素。
type FileInfo struct {
	FileID       int64  `json:"fileId"`
	Filename     string `json:"filename"`
	Type         int    `json:"type"` // 0-文件 1-文件夹
	Size         int64  `json:"size"`
	Etag         string `json:"etag"`
	Status       int    `json:"status"` // >100 表示审核驳回
	ParentFileID int64  `json:"parentFileId"`
	Category     int    `json:"category"`
	Trashed      int    `json:"trashed"` // 1 表示在回收站
	CreateAt     string `json:"createAt"`
	UpdateAt     string `json:"updateAt"`
}

// IsDir 表示该条目是否文件夹。
func (f FileInfo) IsDir() bool { return f.Type == 1 }

// ErrNotConfigured 表示既没有 access_token，也没有 clientID/clientSecret。
var ErrNotConfigured = errors.New("尚未配置 123 云盘 access_token（或 clientID/clientSecret），请先在「设置」中填写")

// APIError 是开放平台返回的业务错误。
type APIError struct {
	Code       int
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	if e.HTTPStatus != 0 && e.Code == 0 {
		return fmt.Sprintf("接口返回 HTTP %d", e.HTTPStatus)
	}
	return fmt.Sprintf("接口返回错误 code=%d: %s", e.Code, e.Message)
}

// authError 表示鉴权失败，可以尝试刷新 token 后重试。
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func isAuthError(err error) bool {
	var ae *authError
	return errors.As(err, &ae)
}

// IsAuthError 表示错误是否属于鉴权类错误（token 无效、权限不足等）。
func IsAuthError(err error) bool { return isAuthError(err) }

// Limiter 是一个简单的匀速限速器（令牌桶的退化版）：保证两次请求之间至少间隔 interval。
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

// Interval 返回当前请求最小间隔。
func (l *Limiter) Interval() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.interval
}

// Wait 阻塞直到获得下一个请求时间片。返回实际等待时长。
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

// Options 是 Client 的配置。
type Options struct {
	BaseURL        string
	AccessToken    string
	TokenExpiredAt time.Time
	ClientID       string
	ClientSecret   string
	QPS            float64
	PageSize       int
	Timeout        time.Duration
	// OnToken 在 token 发生变化（例如自动换取）时回调，用于持久化。
	OnToken func(accessToken string, expiredAt time.Time)
}

// Client 是 123 云盘开放平台客户端。所有请求都经过同一个 Limiter，串行限速。
type Client struct {
	http    *http.Client // 受 mu 保护，Configure 时会整体替换
	limiter *Limiter

	mu           sync.RWMutex
	baseURL      string
	pageSize     int
	token        string
	expiredAt    time.Time
	clientID     string
	clientSecret string
	onToken      func(string, time.Time)

	// OnWait 在因限速需要等待超过 300ms 时回调（在发起请求的 goroutine 中执行）。
	OnWait func(time.Duration)
}

// New 创建客户端。
func New(o Options) *Client {
	c := &Client{http: &http.Client{}, limiter: NewLimiter(o.QPS)}
	c.Configure(o)
	return c
}

// Configure 更新配置（token / 凭证 / QPS / 分页大小 / 地址）。
func (c *Client) Configure(o Options) {
	baseURL := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	pageSize := o.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	c.mu.Lock()
	c.baseURL = baseURL
	c.pageSize = pageSize
	c.token = strings.TrimSpace(o.AccessToken)
	c.expiredAt = o.TokenExpiredAt
	c.clientID = strings.TrimSpace(o.ClientID)
	c.clientSecret = strings.TrimSpace(o.ClientSecret)
	c.onToken = o.OnToken
	c.http = &http.Client{Timeout: timeout}
	c.mu.Unlock()

	c.limiter.SetQPS(o.QPS)
}

// SetQPS 调整限速。
func (c *Client) SetQPS(qps float64) { c.limiter.SetQPS(qps) }

// Interval 返回两次请求之间的最小间隔。
func (c *Client) Interval() time.Duration { return c.limiter.Interval() }

// QPS 返回当前限速值（每秒请求数）。
func (c *Client) QPS() float64 {
	d := c.limiter.Interval()
	if d <= 0 {
		return 1
	}
	return float64(time.Second) / float64(d)
}

// BaseURL 返回接口地址。
func (c *Client) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL
}

// PageSize 返回每页条数。
func (c *Client) PageSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pageSize
}

// HasCredentials 表示是否已经配置了 token 或 clientID/clientSecret。
func (c *Client) HasCredentials() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token != "" || (c.clientID != "" && c.clientSecret != "")
}

// TokenExpiredAt 返回当前 token 的过期时间（可能为零值）。
func (c *Client) TokenExpiredAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.expiredAt
}

// EnsureToken 保证有一个可用的 access_token；如果配置了 clientID/clientSecret
// 且当前 token 为空或已过期，会自动换取并回调 OnToken。
func (c *Client) EnsureToken(ctx context.Context) error {
	c.mu.RLock()
	token, expiredAt := c.token, c.expiredAt
	clientID, clientSecret := c.clientID, c.clientSecret
	c.mu.RUnlock()

	valid := token != ""
	if valid && !expiredAt.IsZero() && !time.Now().Before(expiredAt.Add(-2*time.Minute)) {
		valid = false
	}
	if valid {
		return nil
	}
	if clientID == "" || clientSecret == "" {
		if token != "" {
			// 有过期时间但已过期，仍尝试用旧 token 一次，交给 401 逻辑处理。
			return nil
		}
		return ErrNotConfigured
	}

	if err := c.login(ctx); err != nil {
		return err
	}
	return nil
}

// login 使用 clientID/clientSecret 换取 access_token。
func (c *Client) login(ctx context.Context) error {
	c.mu.RLock()
	clientID, clientSecret, baseURL := c.clientID, c.clientSecret, c.baseURL
	c.mu.RUnlock()

	body, _ := json.Marshal(map[string]string{"clientID": clientID, "clientSecret": clientSecret})
	data, err := c.do(ctx, http.MethodPost, baseURL+"/api/v1/access_token", body, nil)
	if err != nil {
		return fmt.Errorf("换取 access_token 失败: %w", err)
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AccessToken string `json:"accessToken"`
			ExpiredAt   string `json:"expiredAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("解析 access_token 响应失败: %w", err)
	}
	if resp.Code != 0 {
		return &APIError{Code: resp.Code, Message: resp.Message}
	}
	if resp.Data.AccessToken == "" {
		return errors.New("换取 access_token 失败: 响应中没有 accessToken")
	}

	expiredAt := parseTime(resp.Data.ExpiredAt)

	c.mu.Lock()
	c.token = resp.Data.AccessToken
	c.expiredAt = expiredAt
	cb := c.onToken
	c.mu.Unlock()

	if cb != nil {
		cb(resp.Data.AccessToken, expiredAt)
	}
	return nil
}

// invalidateToken 丢弃当前 token，迫使下次请求重新换取。
func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.token = ""
	c.expiredAt = time.Time{}
	c.mu.Unlock()
}

// ListAll 拉取某个目录下的全部文件（自动翻页），并过滤回收站条目。
// parentFileID 为 0 表示根目录。
func (c *Client) ListAll(ctx context.Context, parentFileID int64) ([]FileInfo, error) {
	if err := c.EnsureToken(ctx); err != nil {
		return nil, err
	}

	var (
		all     []FileInfo
		lastID  int64
		page    int
		retried bool
	)
	for {
		files, next, err := c.listPage(ctx, parentFileID, lastID)
		if err != nil && isAuthError(err) && !retried {
			retried = true
			c.invalidateToken()
			if err := c.EnsureToken(ctx); err != nil {
				return nil, err
			}
			files, next, err = c.listPage(ctx, parentFileID, lastID)
		}
		if err != nil {
			return nil, err
		}

		for _, f := range files {
			if f.Trashed != 0 {
				continue
			}
			all = append(all, f)
		}

		page++
		if next < 0 {
			return all, nil
		}
		if next == lastID && page > 1 {
			return nil, fmt.Errorf("翻页异常：lastFileId 未前进（%d）", next)
		}
		lastID = next

		if page > 10000 {
			return nil, errors.New("翻页次数过多，已中止")
		}
	}
}

// listPage 拉取一页文件列表，返回文件和下一页 lastFileId（-1 表示结束）。
func (c *Client) listPage(ctx context.Context, parentFileID, lastFileID int64) ([]FileInfo, int64, error) {
	c.mu.RLock()
	baseURL, pageSize, token := c.baseURL, c.pageSize, c.token
	c.mu.RUnlock()

	q := url.Values{}
	q.Set("parentFileId", strconv.FormatInt(parentFileID, 10))
	q.Set("limit", strconv.Itoa(pageSize))
	if lastFileID > 0 {
		q.Set("lastFileId", strconv.FormatInt(lastFileID, 10))
	}
	u := baseURL + "/api/v2/file/list?" + q.Encode()

	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"Platform":      "open_platform",
		"Accept":        "application/json",
	}

	data, err := c.do(ctx, http.MethodGet, u, nil, headers)
	if err != nil {
		return nil, 0, err
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			LastFileID int64      `json:"lastFileId"`
			FileList   []FileInfo `json:"fileList"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, 0, fmt.Errorf("解析文件列表失败: %w", err)
	}
	if resp.Code != 0 {
		if resp.Code == 401 || resp.Code == 403 || strings.Contains(strings.ToLower(resp.Message), "token") {
			return nil, 0, &authError{msg: fmt.Sprintf("鉴权失败 code=%d: %s", resp.Code, resp.Message)}
		}
		return nil, 0, &APIError{Code: resp.Code, Message: resp.Message}
	}

	next := resp.Data.LastFileID
	if next <= 0 {
		next = -1
	}
	return resp.Data.FileList, next, nil
}

// do 执行一次 HTTP 请求，带限速、超时和重试。
func (c *Client) do(ctx context.Context, method, rawURL string, body []byte, headers map[string]string) ([]byte, error) {
	const maxAttempts = 4

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		waited, err := c.limiter.Wait(ctx)
		if err != nil {
			return nil, err
		}
		if waited > 300*time.Millisecond && c.OnWait != nil {
			c.OnWait(waited)
		}

		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		c.mu.RLock()
		httpClient := c.http
		c.mu.RUnlock()

		resp, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()

		if readErr != nil {
			lastErr = readErr
			continue
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			lastErr = fmt.Errorf("请求过于频繁（HTTP 429）")
			if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(secs) * time.Second):
				}
			}
			continue
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("服务端错误（HTTP %d）", resp.StatusCode)
			continue
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, &authError{msg: fmt.Sprintf("鉴权失败（HTTP %d）", resp.StatusCode)}
		case resp.StatusCode != http.StatusOK:
			return nil, &APIError{HTTPStatus: resp.StatusCode, Message: strings.TrimSpace(string(data))}
		}

		return data, nil
	}
	if lastErr == nil {
		lastErr = errors.New("请求失败")
	}
	return nil, lastErr
}

// parseTime 尽量解析 123 云盘返回的时间字符串。
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
