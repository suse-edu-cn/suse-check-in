package oaa

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeEnvelope(w http.ResponseWriter, status int, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    status,
		"message": message,
		"data":    data,
	})
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	client, err := New(ts.URL+"/", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestLogin(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/auth/login" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("login sent authorization %q", auth)
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req["account"] != "20240001" || req["password"] != "secret" || req["device"] != "checkin-test" {
			t.Errorf("request body = %+v", req)
		}
		writeEnvelope(w, http.StatusOK, "success", map[string]string{
			"token":         "access-1",
			"refresh_token": "refresh-1",
		})
	})
	tokens, err := client.Login(t.Context(), "20240001", "secret", "checkin-test")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Token != "access-1" || tokens.RefreshToken != "refresh-1" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestLoginError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, http.StatusBadRequest, "密码错误", nil)
	})
	_, err := client.Login(t.Context(), "20240001", "bad", "checkin-test")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Message != "密码错误" {
		t.Fatalf("error = %+v", apiErr)
	}
}

func TestRefresh(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req["refresh_token"] != "refresh-1" {
			t.Errorf("refresh token = %q", req["refresh_token"])
		}
		writeEnvelope(w, http.StatusOK, "success", map[string]string{
			"token":         "access-2",
			"refresh_token": "refresh-1",
		})
	})
	tokens, err := client.Refresh(t.Context(), "refresh-1")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Token != "access-2" || tokens.RefreshToken != "refresh-1" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestMeUnauthorized(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, http.StatusUnauthorized, "Token 无效", nil)
	})
	_, err := client.Me(t.Context(), "bad")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

func TestLogout(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Errorf("authorization = %q", got)
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req["device"] != "checkin-test" {
			t.Errorf("device = %q", req["device"])
		}
		writeEnvelope(w, http.StatusOK, "success", nil)
	})
	if err := client.Logout(t.Context(), "access-1", "checkin-test"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCachesAndInvalidates(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeEnvelope(w, http.StatusOK, "success", map[string]any{
			"user_id":    7,
			"student_id": "20240007",
			"name":       "测试用户",
			"role":       "会员",
		})
	})
	user, err := client.Validate(t.Context(), "access-cache")
	if err != nil || user.UserID != 7 || user.Name != "测试用户" {
		t.Fatalf("user = %+v, err = %v", user, err)
	}
	if _, err := client.Validate(t.Context(), "access-cache"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("me calls = %d, want 1", got)
	}
	client.Invalidate("access-cache")
	if _, err := client.Validate(t.Context(), "access-cache"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("me calls after invalidate = %d, want 2", got)
	}
}

func TestValidateDoesNotCacheExpiredToken(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeEnvelope(w, http.StatusOK, "success", map[string]any{"user_id": 1})
	})
	token := testToken(time.Now().Add(-time.Minute))
	for i := 0; i < 2; i++ {
		if _, err := client.Validate(t.Context(), token); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("me calls = %d, want 2", got)
	}
}

func TestTokenExpiry(t *testing.T) {
	want := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	token := testToken(want)
	got, ok := TokenExpiry(token)
	if !ok || !got.Equal(want) {
		t.Fatalf("expiry = %v, %v; want %v", got, ok, want)
	}
	if _, ok := TokenExpiry("not-a-jwt"); ok {
		t.Fatal("invalid token parsed")
	}
}

func TestUpstreamUnavailable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := ts.URL
	ts.Close()
	client, err := New(base, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Me(t.Context(), "access-1")
	if err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidEnvelope(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>not json</html>"))
	})
	_, err := client.Me(t.Context(), "access-1")
	if err == nil || !strings.Contains(err.Error(), "无效响应") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com", "example.com", "http://"} {
		if _, err := New(raw, time.Second); err == nil {
			t.Fatalf("New(%q) succeeded", raw)
		}
	}
}

func testToken(exp time.Time) string {
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix()})
	encode := base64.RawURLEncoding.EncodeToString
	return fmt.Sprintf("%s.%s.sig", encode([]byte(`{"alg":"HS256","typ":"JWT"}`)), encode(payload))
}
