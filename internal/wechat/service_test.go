package wechat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"suse-checkin/internal/store"
	"suse-checkin/internal/upstream"
)

func wechatTestJWT(uid, name, openID string, exp time.Time) string {
	extra, _ := json.Marshal(map[string]string{"openId": openID, "userName": name})
	payload, _ := json.Marshal(map[string]any{
		"uid":   uid,
		"exp":   exp.Unix(),
		"extra": string(extra),
	})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func newWechatFake(t *testing.T, jwt string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/edu/v2/weixin/getClientId", func(w http.ResponseWriter, r *http.Request) {
		writeWechatJSON(w, map[string]any{"code": 200, "msg": "ok", "data": map[string]string{"client_id": "client-1"}})
	})
	mux.HandleFunc("/edu/v2/weixin/getQrCodeUrl", func(w http.ResponseWriter, r *http.Request) {
		writeWechatJSON(w, map[string]any{"code": 200, "msg": "ok", "data": map[string]any{"img": "data:image/png;base64,x", "minute": 5}})
	})
	mux.HandleFunc("/edu/v1/wechat/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data:1,scanned\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data:2,ybClientId=client-1\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})
	mux.HandleFunc("/callback/edu/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "_sop_session_", Value: jwt, Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/site/appware/system/sso/loginUrl", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("service"), "open_id=open-1") {
			http.Error(w, "bad service", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "session-1", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux)
}

func writeWechatJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func waitWechatStatus(t *testing.T, svc *Service, id string, owner uint64) LoginView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := svc.Status(id, owner)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == StatusSuccess {
			return view
		}
		if view.Status == StatusExpired || view.Status == StatusError {
			t.Fatalf("login failed: %+v", view)
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("wechat login did not complete")
	return LoginView{}
}

func TestWechatServiceComplete(t *testing.T) {
	jwt := wechatTestJWT("20240001", "扫码用户", "open-1", time.Now().Add(48*time.Hour))
	ts := newWechatFake(t, jwt)
	defer ts.Close()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := New(upstream.New(ts.URL, ts.URL, 2*time.Second), st, nil)
	defer svc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start, err := svc.Start(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if start.Status != StatusWaiting || start.QRCode == "" {
		t.Fatalf("start = %+v", start)
	}
	if _, err := svc.Status(start.ID, 8); err != ErrNotFound {
		t.Fatalf("owner mismatch error = %v", err)
	}
	if _, err := svc.Complete(start.ID, 7, CompleteInput{Campus: "宜宾"}); err == nil {
		t.Fatal("completed before scan")
	}

	success := waitWechatStatus(t, svc, start.ID, 7)
	if success.StudentID != "20240001" || success.Name != "扫码用户" {
		t.Fatalf("success = %+v", success)
	}
	view, err := svc.Complete(start.ID, 7, CompleteInput{Name: "扫码用户", Campus: "宜宾"})
	if err != nil {
		t.Fatal(err)
	}
	if view.LoginType != store.LoginWechat || view.StudentID != "20240001" {
		t.Fatalf("account = %+v", view)
	}
	acc, ok := st.Account(7, view.ID)
	if !ok {
		t.Fatal("account missing")
	}
	cookies, err := st.RememberCookies(acc)
	if err != nil || !strings.Contains(cookies, "SESSION=session-1") {
		t.Fatalf("cookies = %q, %v", cookies, err)
	}
	if _, err := svc.Status(start.ID, 7); err != ErrNotFound {
		t.Fatalf("status after complete = %v", err)
	}
}

func TestWechatServiceRescanUpdatesAccount(t *testing.T) {
	jwt := wechatTestJWT("20240002", "扫码用户", "open-1", time.Now().Add(48*time.Hour))
	ts := newWechatFake(t, jwt)
	defer ts.Close()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acc, err := st.AddWechatAccount(7, store.WechatAccountInput{
		StudentID: "20240002",
		Name:      "扫码用户",
		Campus:    "宜宾",
		Cookies:   "_sop_session_=old; SESSION=old",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := New(upstream.New(ts.URL, ts.URL, 2*time.Second), st, nil)
	defer svc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start, err := svc.Start(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	waitWechatStatus(t, svc, start.ID, 7)
	view, err := svc.Complete(start.ID, 7, CompleteInput{AccountID: acc.ID, Name: "新名字", Campus: "汇东"})
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != acc.ID || view.Name != "新名字" || view.Campus != "汇东" {
		t.Fatalf("view = %+v", view)
	}
	updated, _ := st.Account(7, acc.ID)
	cookies, _ := st.RememberCookies(updated)
	if !strings.Contains(cookies, "SESSION=session-1") {
		t.Fatalf("cookies = %q", cookies)
	}
}

func TestWechatServiceCancel(t *testing.T) {
	jwt := wechatTestJWT("20240003", "扫码用户", "open-1", time.Now().Add(48*time.Hour))
	ts := newWechatFake(t, jwt)
	defer ts.Close()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := New(upstream.New(ts.URL, ts.URL, 2*time.Second), st, nil)
	defer svc.Close()
	start, err := svc.Start(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	svc.Cancel(start.ID, 7)
	if _, err := svc.Status(start.ID, 7); err != ErrNotFound {
		t.Fatalf("status after cancel = %v", err)
	}
}
