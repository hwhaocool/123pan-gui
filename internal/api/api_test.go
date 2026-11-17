package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListAllPaginationAndTrashedFilter(t *testing.T) {
	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		if r.URL.Path != "/api/v2/file/list" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Platform"); got != "open_platform" {
			t.Errorf("Platform = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Errorf("limit = %q", got)
		}

		var data string
		switch r.URL.Query().Get("lastFileId") {
		case "":
			data = `{"code":0,"message":"ok","data":{"lastFileId":111,"fileList":[
				{"fileId":1,"filename":"a.txt","type":0,"size":10,"trashed":0},
				{"fileId":2,"filename":"dir","type":1,"size":0,"trashed":0}]}}`
		case "111":
			data = `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[
				{"fileId":3,"filename":"trash.txt","type":0,"size":1,"trashed":1},
				{"fileId":4,"filename":"b.txt","type":0,"size":5,"trashed":0}]}}`
		default:
			t.Errorf("unexpected lastFileId: %s", r.URL.Query().Get("lastFileId"))
			data = `{"code":0,"data":{"lastFileId":-1,"fileList":[]}}`
		}
		fmt.Fprint(w, data)
	}))
	defer srv.Close()

	c := New(Options{
		BaseURL:     srv.URL,
		AccessToken: "test-token",
		QPS:         1000,
		PageSize:    2,
	})

	files, err := c.ListAll(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3 (trashed filtered)", len(files))
	}

	ids := []int64{files[0].FileID, files[1].FileID, files[2].FileID}
	want := []int64{1, 2, 4}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}

func TestLoginAndTokenCallback(t *testing.T) {
	var gotToken string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/access_token":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			var body struct {
				ClientID     string `json:"clientID"`
				ClientSecret string `json:"clientSecret"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			if body.ClientID != "cid" || body.ClientSecret != "secret" {
				t.Errorf("credentials = %q/%q", body.ClientID, body.ClientSecret)
			}
			fmt.Fprint(w, `{"code":0,"message":"ok","data":{"accessToken":"fresh-token","expiredAt":"2035-01-01T00:00:00Z"}}`)
		case "/api/v2/file/list":
			gotToken = r.Header.Get("Authorization")
			fmt.Fprint(w, `{"code":0,"message":"ok","data":{"lastFileId":-1,"fileList":[]}}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	var cbToken string
	var cbExpired time.Time

	c := New(Options{
		BaseURL:      srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
		QPS:          1000,
		OnToken: func(token string, expiredAt time.Time) {
			cbToken, cbExpired = token, expiredAt
		},
	})

	if _, err := c.ListAll(context.Background(), 0); err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if gotToken != "Bearer fresh-token" {
		t.Fatalf("Authorization = %q", gotToken)
	}
	if cbToken != "fresh-token" {
		t.Fatalf("OnToken token = %q", cbToken)
	}
	if cbExpired.IsZero() {
		t.Fatal("OnToken expiredAt is zero")
	}
}

func TestNotConfigured(t *testing.T) {
	c := New(Options{BaseURL: "http://127.0.0.1:1", QPS: 1000})
	if _, err := c.ListAll(context.Background(), 0); err == nil {
		t.Fatal("expected error when no credentials are configured")
	}
}

func TestLimiterInterval(t *testing.T) {
	l := NewLimiter(2)
	if got := l.Interval(); got != 500*time.Millisecond {
		t.Fatalf("interval = %s, want 500ms", got)
	}
	l.SetQPS(1)
	if got := l.Interval(); got != time.Second {
		t.Fatalf("interval = %s, want 1s", got)
	}
}

func TestListAllReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":5113,"message":"参数错误"}`)
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, AccessToken: "t", QPS: 1000})
	if _, err := c.ListAll(context.Background(), 0); err == nil {
		t.Fatal("expected API error")
	}
}
