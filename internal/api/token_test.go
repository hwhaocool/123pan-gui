package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// tokenServer 是一个假的 123 云盘开放平台：/api/v1/access_token + /api/v2/file/list。
type tokenServer struct {
	srv *httptest.Server

	mu        sync.Mutex
	logins    int
	platforms []string
	auths     []string
	// loginResp 可覆盖默认的 token 响应。
	loginResp string
}

func newTokenServer(t *testing.T) *tokenServer {
	t.Helper()
	ts := &tokenServer{}

	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()

		switch r.URL.Path {
		case "/api/v1/access_token":
			if r.Method != http.MethodPost {
				t.Errorf("access_token method = %s", r.Method)
			}
			ts.logins++
			ts.platforms = append(ts.platforms, r.Header.Get("Platform"))

			var body struct {
				ClientID     string `json:"clientID"`
				ClientSecret string `json:"clientSecret"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode login body: %v", err)
			}
			if body.ClientID != "cid" || body.ClientSecret != "secret" {
				t.Errorf("login body = %q/%q", body.ClientID, body.ClientSecret)
			}

			if ts.loginResp != "" {
				fmt.Fprint(w, ts.loginResp)
				return
			}
			expiredAt := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
			fmt.Fprintf(w, `{"code":0,"message":"ok","data":{"accessToken":"tok-%d","expiredAt":%q}}`, ts.logins, expiredAt)

		case "/api/v2/file/list":
			ts.auths = append(ts.auths, r.Header.Get("Authorization"))
			if r.Header.Get("Platform") != "open_platform" {
				t.Errorf("list Platform = %q", r.Header.Get("Platform"))
			}
			fmt.Fprint(w, `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[]}}`)

		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func (ts *tokenServer) snapshot() (logins int, platforms, auths []string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.logins, append([]string(nil), ts.platforms...), append([]string(nil), ts.auths...)
}

// TestEnsureTokenFetchesWhenExpired 验证：token 已过期时，先自动换取再请求列表。
func TestEnsureTokenFetchesWhenExpired(t *testing.T) {
	ts := newTokenServer(t)

	var tokenCalls int
	var savedToken string
	var savedExpiredAt time.Time

	c := New(Options{
		BaseURL:        ts.srv.URL,
		AccessToken:    "stale-token",
		TokenExpiredAt: time.Now().Add(-time.Hour),
		ClientID:       "cid",
		ClientSecret:   "secret",
		QPS:            1000,
		OnToken: func(token string, expiredAt time.Time) {
			tokenCalls++
			savedToken, savedExpiredAt = token, expiredAt
		},
	})

	if _, err := c.ListAll(context.Background(), 0); err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	logins, platforms, auths := ts.snapshot()
	if logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
	if len(platforms) != 1 || platforms[0] != "open_platform" {
		t.Fatalf("login Platform headers = %v", platforms)
	}
	if len(auths) != 1 || auths[0] != "Bearer tok-1" {
		t.Fatalf("list Authorization = %v, want Bearer tok-1", auths)
	}
	if tokenCalls != 1 || savedToken != "tok-1" {
		t.Fatalf("OnToken calls=%d token=%q", tokenCalls, savedToken)
	}
	if savedExpiredAt.IsZero() {
		t.Fatal("OnToken expiredAt is zero")
	}

	// 第二次请求：token 仍然有效，不应再次换取。
	if _, err := c.ListAll(context.Background(), 0); err != nil {
		t.Fatalf("second ListAll: %v", err)
	}
	if logins, _, auths := ts.snapshot(); logins != 1 || auths[len(auths)-1] != "Bearer tok-1" {
		t.Fatalf("after second call logins=%d auths=%v", logins, auths)
	}
}

// TestEnsureTokenRefreshesBeforeExpiry 验证：token 5 分钟内就要过期时会提前续期。
func TestEnsureTokenRefreshesBeforeExpiry(t *testing.T) {
	ts := newTokenServer(t)

	c := New(Options{
		BaseURL:        ts.srv.URL,
		AccessToken:    "about-to-expire",
		TokenExpiredAt: time.Now().Add(time.Minute),
		ClientID:       "cid",
		ClientSecret:   "secret",
		QPS:            1000,
	})

	if !c.NeedsRefresh() {
		t.Fatal("token expiring in 1 minute should need refresh")
	}
	if _, err := c.ListAll(context.Background(), 0); err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	logins, _, auths := ts.snapshot()
	if logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
	if auths[0] != "Bearer tok-1" {
		t.Fatalf("Authorization = %q, want renewed token", auths[0])
	}
}

// TestEnsureTokenKeepsValidToken 验证：token 还有效时不会浪费一次换取请求。
func TestEnsureTokenKeepsValidToken(t *testing.T) {
	ts := newTokenServer(t)

	expiredAt := time.Now().Add(2 * time.Hour)
	c := New(Options{
		BaseURL:        ts.srv.URL,
		AccessToken:    "good-token",
		TokenExpiredAt: expiredAt,
		ClientID:       "cid",
		ClientSecret:   "secret",
		QPS:            1000,
	})

	if c.NeedsRefresh() {
		t.Fatal("valid token should not need refresh")
	}
	if _, err := c.ListAll(context.Background(), 0); err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	logins, _, auths := ts.snapshot()
	if logins != 0 {
		t.Fatalf("logins = %d, want 0", logins)
	}
	if len(auths) != 1 || auths[0] != "Bearer good-token" {
		t.Fatalf("Authorization = %v", auths)
	}
}

// TestRefreshTokenForcesFetch 验证「立即获取」会强制换新 token。
func TestRefreshTokenForcesFetch(t *testing.T) {
	ts := newTokenServer(t)

	c := New(Options{
		BaseURL:        ts.srv.URL,
		AccessToken:    "still-valid",
		TokenExpiredAt: time.Now().Add(4 * time.Hour),
		ClientID:       "cid",
		ClientSecret:   "secret",
		QPS:            1000,
	})

	expiredAt, err := c.RefreshToken(context.Background())
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if !expiredAt.After(time.Now()) {
		t.Fatalf("expiredAt = %s", expiredAt)
	}

	logins, _, _ := ts.snapshot()
	if logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
	if token, _ := c.TokenState(); token != "tok-1" {
		t.Fatalf("token = %q", token)
	}
}

// TestRefreshTokenWithoutCredentials 验证缺少凭证时给出明确错误。
func TestRefreshTokenWithoutCredentials(t *testing.T) {
	c := New(Options{BaseURL: "http://127.0.0.1:1", QPS: 1000})
	if _, err := c.RefreshToken(context.Background()); err == nil {
		t.Fatal("expected error without clientID/clientSecret")
	}
}

// TestInvalidTokenTriggersRefresh 验证接口返回 401 时会清掉旧 token、换取新 token 后重试。
func TestInvalidTokenTriggersRefresh(t *testing.T) {
	var (
		mu        sync.Mutex
		logins    int
		listCalls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/api/v1/access_token":
			logins++
			fmt.Fprint(w, `{"code":0,"message":"ok","data":{"accessToken":"renewed","expiredAt":"`+
				time.Now().Add(time.Hour).Format(time.RFC3339)+`"}}`)
		case "/api/v2/file/list":
			listCalls++
			if r.Header.Get("Authorization") == "Bearer broken" {
				fmt.Fprint(w, `{"code":401,"message":"token is invalid"}`)
				return
			}
			fmt.Fprint(w, `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[]}}`)
		}
	}))
	defer srv.Close()

	c := New(Options{
		BaseURL:        srv.URL,
		AccessToken:    "broken",
		TokenExpiredAt: time.Now().Add(time.Hour), // 认为还有效，靠 401 触发续期
		ClientID:       "cid",
		ClientSecret:   "secret",
		QPS:            1000,
	})

	if _, err := c.ListAll(context.Background(), 0); err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if logins != 1 || listCalls != 2 {
		t.Fatalf("logins=%d listCalls=%d, want 1/2", logins, listCalls)
	}
	if token, _ := c.TokenState(); token != "renewed" {
		t.Fatalf("token = %q", token)
	}
}

// TestLoginErrorResponse 验证换取 token 失败时的错误信息。
func TestLoginErrorResponse(t *testing.T) {
	ts := newTokenServer(t)
	ts.loginResp = `{"code":400,"message":"clientID 无效"}`

	c := New(Options{
		BaseURL:      ts.srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
		QPS:          1000,
	})

	if _, err := c.ListAll(context.Background(), 0); err == nil {
		t.Fatal("expected login error")
	}
}
