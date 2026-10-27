package upstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testSopJWT(uid, name, openID string, exp time.Time) string {
	extra, _ := json.Marshal(map[string]string{"openId": openID, "userName": name})
	payload, _ := json.Marshal(map[string]any{
		"uid":   uid,
		"exp":   exp.Unix(),
		"extra": string(extra),
	})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

type fakeQfhy struct {
	t       *testing.T
	mu      sync.Mutex
	referer string
	sopJWT  string
}

func (f *fakeQfhy) server() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/edu/v2/weixin/getClientId", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/edu/v2/weixin/getClientId" {
			f.t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{
			"code": 200,
			"msg":  "ok",
			"data": map[string]string{"client_id": "client-1"},
		})
	})
	mux.HandleFunc("/edu/v2/weixin/getQrCodeUrl", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Error(err)
		}
		if body["client_id"] != "client-1" {
			f.t.Errorf("client id = %q", body["client_id"])
		}
		writeJSON(w, 200, map[string]any{
			"code": 200,
			"msg":  "ok",
			"data": map[string]any{
				"img":    "data:image/png;base64,iVBORw0KGgo=",
				"minute": 5,
			},
		})
	})
	mux.HandleFunc("/edu/v1/wechat/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("clientId") != "client-1" {
			f.t.Errorf("stream clientId = %q", r.URL.Query().Get("clientId"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data:1,scanned\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data:2,ybClientId=client-1\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})
	mux.HandleFunc("/callback/edu/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "_sop_session_", Value: f.sopJWT, Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/site/appware/system/sso/loginUrl", func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		if !strings.Contains(service, "open_id=open-1") {
			f.t.Errorf("sso service = %q", service)
		}
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "session-1", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/site/qddk/qdrw/api/myList.rst", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.referer = r.Header.Get("Referer")
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"success":    true,
			"resultCode": 0,
			"result":     map[string]any{"data": []any{}},
		})
	})
	return httptest.NewServer(mux)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestWechatLoginFlow(t *testing.T) {
	expires := time.Now().Add(48 * time.Hour)
	fake := &fakeQfhy{t: t, sopJWT: testSopJWT("20240001", "扫码用户", "open-1", expires)}
	ts := fake.server()
	defer ts.Close()

	client := New(ts.URL, ts.URL, 2*time.Second)
	login, err := client.StartWechatLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(login.QRCode(), "data:image/png;base64,") {
		t.Fatalf("qr = %q", login.QRCode())
	}
	if time.Until(login.ExpiresAt()) <= 0 {
		t.Fatal("qr already expired")
	}

	var statuses []int
	result, err := login.Wait(context.Background(), func(status int) {
		statuses = append(statuses, status)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || statuses[0] != 1 || statuses[1] != 2 {
		t.Fatalf("statuses = %v", statuses)
	}
	if result.StudentID != "20240001" || result.Name != "扫码用户" || result.OpenID != "open-1" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Cookies, "_sop_session_=") || !strings.Contains(result.Cookies, "SESSION=session-1") {
		t.Fatalf("cookies = %q", result.Cookies)
	}
	if !result.ExpiresAt.Equal(expires.Truncate(time.Second)) {
		t.Fatalf("expires = %v", result.ExpiresAt)
	}

	session, updated, sessionExpires, err := client.WechatSession(context.Background(), result.Cookies)
	if err != nil {
		t.Fatal(err)
	}
	if session == nil || !strings.Contains(updated, "SESSION=session-1") {
		t.Fatalf("session = %v, cookies = %q", session, updated)
	}
	if !sessionExpires.Equal(expires.Truncate(time.Second)) {
		t.Fatalf("session expires = %v", sessionExpires)
	}
	if _, err := session.PendingTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	referer := fake.referer
	fake.mu.Unlock()
	if !strings.Contains(referer, "open_id=open-1") {
		t.Fatalf("referer = %q", referer)
	}
}

func TestWechatSessionExpired(t *testing.T) {
	client := New("http://example.invalid", "http://example.invalid", time.Second)
	expired := testSopJWT("20240001", "扫码用户", "open-1", time.Now().Add(-time.Hour))
	_, _, _, err := client.WechatSession(context.Background(), "_sop_session_="+expired+"; SESSION=old")
	if err == nil || !strings.Contains(err.Error(), "扫码会话已过期") {
		t.Fatalf("err = %v", err)
	}
}

func TestWechatCancelled(t *testing.T) {
	fake := &fakeQfhy{t: t, sopJWT: testSopJWT("20240001", "扫码用户", "open-1", time.Now().Add(time.Hour))}
	ts := fake.server()
	defer ts.Close()
	// 这里只验证状态事件解析：单独启动一个返回取消事件的流。
	mux := http.NewServeMux()
	mux.HandleFunc("/edu/v2/weixin/getClientId", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"code": 200, "data": map[string]string{"client_id": "client-1"}})
	})
	mux.HandleFunc("/edu/v2/weixin/getQrCodeUrl", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"code": 200, "data": map[string]any{"img": "data:image/png;base64,x", "minute": 5}})
	})
	mux.HandleFunc("/edu/v1/wechat/stream", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "data:3,cancelled\n\n")
	})
	cancelServer := httptest.NewServer(mux)
	defer cancelServer.Close()
	client := New(cancelServer.URL, cancelServer.URL, 2*time.Second)
	login, err := client.StartWechatLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := login.Wait(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseSopSessionVariants(t *testing.T) {
	extra, _ := json.Marshal(map[string]string{"openId": "open-2", "userName": "测试"})
	payload, _ := json.Marshal(map[string]any{
		"uid":   20240002,
		"extra": string(extra),
	})
	jwt := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	claims, err := parseSopSession(jwt)
	if err != nil {
		t.Fatal(err)
	}
	if claims.StudentID != "20240002" || claims.Name != "测试" || claims.OpenID != "open-2" {
		t.Fatalf("claims = %+v", claims)
	}
	if _, err := parseSopSession("bad"); err == nil {
		t.Fatal("bad jwt accepted")
	}
}
