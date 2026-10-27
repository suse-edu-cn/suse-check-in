package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"suse-checkin/internal/checkin"
	"suse-checkin/internal/config"
	"suse-checkin/internal/oaa"
	"suse-checkin/internal/scheduler"
	"suse-checkin/internal/store"
	"suse-checkin/internal/upstream"
	"suse-checkin/internal/wechat"
)

const (
	testOwner       uint64 = 42
	testOAAAccount         = "20240000"
	testOAAPassword        = "oaa-secret"
	testOAADevice          = "checkin-test"

	testOtherOwner       uint64 = 43
	testOtherOAAAccount         = "20240001"
	testOtherOAAPassword        = "oaa-secret-2"
)

type testRecognizer struct{}

func (testRecognizer) Recognize([]byte) (string, error) { return "1234", nil }

type oaaFixture struct {
	password  string
	userID    uint64
	studentID string
	name      string
	email     string
}

type fakeOAA struct {
	url string

	mu               sync.Mutex
	seq              int
	revokedAccess    map[string]bool
	revokedRefresh   map[string]bool
	refreshByAccess  map[string]string
	accountByAccess  map[string]string
	accountByRefresh map[string]string
	fixtures         map[string]oaaFixture
}

func newFakeOAA(t *testing.T) *fakeOAA {
	t.Helper()
	f := &fakeOAA{
		revokedAccess:    make(map[string]bool),
		revokedRefresh:   make(map[string]bool),
		refreshByAccess:  make(map[string]string),
		accountByAccess:  make(map[string]string),
		accountByRefresh: make(map[string]string),
		fixtures: map[string]oaaFixture{
			testOAAAccount: {
				password: testOAAPassword, userID: testOwner,
				studentID: testOAAAccount, name: "测试用户", email: "tester@example.com",
			},
			testOtherOAAAccount: {
				password: testOtherOAAPassword, userID: testOtherOwner,
				studentID: testOtherOAAAccount, name: "另一个用户", email: "other@example.com",
			},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(ts.Close)
	f.url = ts.URL
	return f
}

func writeOAAResponse(w http.ResponseWriter, status int, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    status,
		"message": message,
		"data":    data,
	})
}

func (f *fakeOAA) issueLocked(account string) (string, string) {
	f.seq++
	access := fmt.Sprintf("access-%d", f.seq)
	refresh := fmt.Sprintf("refresh-%d", f.seq)
	f.refreshByAccess[access] = refresh
	f.accountByAccess[access] = account
	f.accountByRefresh[refresh] = account
	return access, refresh
}

func (f *fakeOAA) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v2/auth/login":
		var req struct {
			Account  string `json:"account"`
			Password string `json:"password"`
			Device   string `json:"device"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOAAResponse(w, http.StatusBadRequest, "获取json失败", nil)
			return
		}
		fixture, ok := f.fixtures[req.Account]
		if !ok || req.Password != fixture.password {
			writeOAAResponse(w, http.StatusBadRequest, "密码错误", nil)
			return
		}
		if req.Device == "" {
			writeOAAResponse(w, http.StatusBadRequest, "获取json失败", nil)
			return
		}
		f.mu.Lock()
		access, refresh := f.issueLocked(req.Account)
		f.mu.Unlock()
		writeOAAResponse(w, http.StatusOK, "success", map[string]string{
			"token":         access,
			"refresh_token": refresh,
		})
	case "/v2/user/me":
		access := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if access == "access-unavailable" {
			writeOAAResponse(w, http.StatusServiceUnavailable, "服务暂不可用", nil)
			return
		}
		f.mu.Lock()
		revoked := f.revokedAccess[access]
		_, issued := f.refreshByAccess[access]
		account := f.accountByAccess[access]
		fixture, known := f.fixtures[account]
		f.mu.Unlock()
		if access == "" || revoked || !issued || !known {
			writeOAAResponse(w, http.StatusUnauthorized, "Token 无效", nil)
			return
		}
		writeOAAResponse(w, http.StatusOK, "success", map[string]any{
			"user_id":    fixture.userID,
			"student_id": fixture.studentID,
			"username":   "tester",
			"name":       fixture.name,
			"email":      fixture.email,
			"department": "技术部",
			"role":       "会员",
			"role_level": 10,
			"avatar": map[string]string{
				"uri": "avatar/default.png",
				"url": "http://example.com/avatar.png",
			},
		})
	case "/v2/auth/refresh":
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOAAResponse(w, http.StatusBadRequest, "获取json失败", nil)
			return
		}
		f.mu.Lock()
		account, bound := f.accountByRefresh[req.RefreshToken]
		valid := bound && strings.HasPrefix(req.RefreshToken, "refresh-") && !f.revokedRefresh[req.RefreshToken]
		access := ""
		if valid {
			f.seq++
			access = fmt.Sprintf("access-%d", f.seq)
			f.refreshByAccess[access] = req.RefreshToken
			f.accountByAccess[access] = account
		}
		f.mu.Unlock()
		if !valid {
			writeOAAResponse(w, http.StatusBadRequest, "refresh_token不存在或者过期", nil)
			return
		}
		writeOAAResponse(w, http.StatusOK, "success", map[string]string{
			"token":         access,
			"refresh_token": req.RefreshToken,
		})
	case "/v2/auth/logout":
		access := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var req struct {
			Device string `json:"device"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOAAResponse(w, http.StatusBadRequest, "获取json失败", nil)
			return
		}
		f.mu.Lock()
		f.revokedAccess[access] = true
		if refresh, ok := f.refreshByAccess[access]; ok {
			f.revokedRefresh[refresh] = true
		}
		f.mu.Unlock()
		writeOAAResponse(w, http.StatusOK, "success", nil)
	default:
		http.NotFound(w, r)
	}
}

func newTestServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	return newTestServerWithNotify(t, true)
}

type fakeWechatService struct {
	startView    wechat.LoginView
	startErr     error
	statusView   wechat.LoginView
	statusErr    error
	completeView store.AccountView
	completeErr  error
	lastOwner    uint64
	starts       int
	cancels      int
}

func (f *fakeWechatService) Start(_ context.Context, ownerID uint64) (wechat.LoginView, error) {
	f.starts++
	f.lastOwner = ownerID
	return f.startView, f.startErr
}

func (f *fakeWechatService) Status(_ string, ownerID uint64) (wechat.LoginView, error) {
	f.lastOwner = ownerID
	return f.statusView, f.statusErr
}

func (f *fakeWechatService) Complete(_ string, ownerID uint64, _ wechat.CompleteInput) (store.AccountView, error) {
	f.lastOwner = ownerID
	return f.completeView, f.completeErr
}

func (f *fakeWechatService) Cancel(_ string, ownerID uint64) {
	f.cancels++
	f.lastOwner = ownerID
}

func defaultFakeWechat() *fakeWechatService {
	return &fakeWechatService{
		startView: wechat.LoginView{
			ID:        "scan-1",
			Status:    wechat.StatusWaiting,
			QRCode:    "data:image/png;base64,x",
			ExpiresAt: time.Now().Add(5 * time.Minute),
		},
		statusView: wechat.LoginView{
			ID:        "scan-1",
			Status:    wechat.StatusSuccess,
			StudentID: "20240000",
			Name:      "扫码用户",
			ExpiresAt: time.Now().Add(5 * time.Minute),
		},
		completeView: store.AccountView{
			ID:        1,
			StudentID: "20240000",
			Name:      "扫码用户",
			LoginType: store.LoginWechat,
			Campus:    "宜宾",
		},
	}
}

func newTestServerWithNotify(t *testing.T, notificationsAvailable bool) (*Server, *store.Store, string) {
	t.Helper()
	return buildTestServer(t, t.TempDir(), notificationsAvailable)
}

func buildTestServer(t *testing.T, dir string, notificationsAvailable bool) (*Server, *store.Store, string) {
	t.Helper()
	fake := newFakeOAA(t)
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Addr:        "127.0.0.1:8099",
		DataDir:     dir,
		Timezone:    "Asia/Shanghai",
		UIASBase:    "http://127.0.0.1:1",
		QFHYBase:    "http://127.0.0.1:1",
		OAABase:     fake.url,
		HTTPTimeout: 2 * time.Second,
	}
	client := upstream.New(cfg.UIASBase, cfg.QFHYBase, cfg.HTTPTimeout)
	svc, err := checkin.New(checkin.Options{
		Store:        st,
		Client:       client,
		Recognizer:   testRecognizer{},
		DataDir:      dir,
		Location:     loc,
		AccountDelay: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	sched := scheduler.New(st, svc, nil, loc, nil)
	oaaClient, err := oaa.New(cfg.OAABase, cfg.HTTPTimeout)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg, st, svc, sched, oaaClient, defaultFakeWechat(), notificationsAvailable, nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st, dir
}

func newTestHTTP(t *testing.T) (*httptest.Server, *Server, *store.Store, string) {
	t.Helper()
	return newTestHTTPWithNotify(t, true)
}

func newTestHTTPWithNotify(t *testing.T, notificationsAvailable bool) (*httptest.Server, *Server, *store.Store, string) {
	t.Helper()
	srv, st, dir := newTestServerWithNotify(t, notificationsAvailable)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, st, dir
}

func newAuthedTestHTTP(t *testing.T) (*httptest.Server, string, *Server, *store.Store, string) {
	t.Helper()
	return newAuthedTestHTTPWithNotify(t, true)
}

func newAuthedTestHTTPWithNotify(t *testing.T, notificationsAvailable bool) (*httptest.Server, string, *Server, *store.Store, string) {
	t.Helper()
	ts, srv, st, dir := newTestHTTPWithNotify(t, notificationsAvailable)
	token, _, _ := loginTest(t, ts)
	return ts, token, srv, st, dir
}

func accountBody(studentID string) map[string]any {
	return map[string]any{
		"studentId": studentID,
		"password":  "super-secret",
		"name":      "测试账号",
		"campus":    "宜宾",
	}
}

func requestJSON(t *testing.T, client *http.Client, method, url string, body any, token string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func jsonRequest(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	return requestJSON(t, client, method, url, body, "")
}

func authRequest(t *testing.T, client *http.Client, token, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	return requestJSON(t, client, method, url, body, token)
}

type loginResult struct {
	Token        string   `json:"token"`
	RefreshToken string   `json:"refresh_token"`
	User         oaa.User `json:"user"`
}

func loginAs(t *testing.T, ts *httptest.Server, account, password string) loginResult {
	t.Helper()
	resp, raw := jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"account":  account,
		"password": password,
		"device":   testOAADevice,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s status = %d, body = %s", account, resp.StatusCode, raw)
	}
	var out loginResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" || out.RefreshToken == "" {
		t.Fatalf("login tokens missing: %s", raw)
	}
	return out
}

func loginTest(t *testing.T, ts *httptest.Server) (string, string, oaa.User) {
	t.Helper()
	out := loginAs(t, ts, testOAAAccount, testOAAPassword)
	return out.Token, out.RefreshToken, out.User
}

func createAccount(t *testing.T, ts *httptest.Server, token, studentID string) store.AccountView {
	t.Helper()
	resp, raw := authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/accounts", accountBody(studentID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", resp.StatusCode, raw)
	}
	var view store.AccountView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestAuthRequired(t *testing.T) {
	ts, _, _, _ := newTestHTTP(t)
	protected := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/state", nil},
		{http.MethodGet, "/api/auth/me", nil},
		{http.MethodPost, "/api/accounts", accountBody("20240090")},
		{http.MethodPut, "/api/schedule", map[string]any{}},
		{http.MethodPost, "/api/run", map[string]any{"accountIds": []int64{1}}},
		{http.MethodGet, "/api/samples/2026-10-26/1-test.jpg", nil},
	}
	for _, tc := range protected {
		resp, raw := jsonRequest(t, ts.Client(), tc.method, ts.URL+tc.path, tc.body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, body = %s", tc.method, tc.path, resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), `"error"`) {
			t.Fatalf("%s %s body = %s", tc.method, tc.path, raw)
		}
	}

	resp, _ := authRequest(t, ts.Client(), "bad-token", http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d", resp.StatusCode)
	}

	resp, _ = jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/api/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	resp, _ = jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d", resp.StatusCode)
	}
}

func TestWechatLoginAPI(t *testing.T) {
	ts, token, srv, _, _ := newAuthedTestHTTP(t)
	fake := srv.wechat.(*fakeWechatService)

	resp, raw := authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/wechat/login", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", resp.StatusCode, raw)
	}
	var view wechat.LoginView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.ID != "scan-1" || view.QRCode == "" || fake.starts != 1 || fake.lastOwner != 42 {
		t.Fatalf("view = %+v, fake = %+v", view, fake)
	}

	resp, raw = authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/wechat/login/scan-1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Status != wechat.StatusSuccess || view.StudentID != "20240000" {
		t.Fatalf("status view = %+v", view)
	}

	resp, raw = authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/wechat/login/scan-1/complete", map[string]any{
		"accountId": 0,
		"name":      "扫码用户",
		"campus":    "宜宾",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", resp.StatusCode, raw)
	}
	var account store.AccountView
	if err := json.Unmarshal(raw, &account); err != nil {
		t.Fatal(err)
	}
	if account.LoginType != store.LoginWechat || account.StudentID != "20240000" {
		t.Fatalf("account = %+v", account)
	}

	resp, _ = authRequest(t, ts.Client(), token, http.MethodDelete, ts.URL+"/api/wechat/login/scan-1", nil)
	if resp.StatusCode != http.StatusNoContent || fake.cancels != 1 {
		t.Fatalf("cancel status = %d, cancels = %d", resp.StatusCode, fake.cancels)
	}

	resp, _ = jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/wechat/login", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth start status = %d", resp.StatusCode)
	}
}

func TestAuthLoginRefreshLogout(t *testing.T) {
	ts, _, _, _ := newTestHTTP(t)

	resp, raw := jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"account":  testOAAAccount,
		"password": "wrong",
		"device":   testOAADevice,
	})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "密码错误") {
		t.Fatalf("wrong password status = %d, body = %s", resp.StatusCode, raw)
	}

	resp, raw = jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/login", map[string]string{
		"account":  testOAAAccount,
		"password": testOAAPassword,
		"device":   "bad device!",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad device status = %d, body = %s", resp.StatusCode, raw)
	}

	token, refreshToken, user := loginTest(t, ts)
	if user.UserID != 42 || user.Name != "测试用户" || user.StudentID != testOAAAccount {
		t.Fatalf("login user = %+v", user)
	}

	resp, raw = authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/auth/me", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "测试用户") {
		t.Fatalf("me status = %d, body = %s", resp.StatusCode, raw)
	}

	resp, raw = jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/refresh", map[string]string{
		"refresh_token": refreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", resp.StatusCode, raw)
	}
	var refreshed struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.Token == "" || refreshed.Token == token || refreshed.RefreshToken != refreshToken {
		t.Fatalf("refreshed = %+v", refreshed)
	}

	resp, _ = authRequest(t, ts.Client(), refreshed.Token, http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state with refreshed token status = %d", resp.StatusCode)
	}

	resp, _ = jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/refresh", map[string]string{
		"refresh_token": "missing",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad refresh status = %d", resp.StatusCode)
	}

	resp, _ = jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/logout", map[string]string{
		"refresh_token": refreshToken,
		"device":        testOAADevice,
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}

	resp, _ = jsonRequest(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/refresh", map[string]string{
		"refresh_token": refreshToken,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh after logout status = %d", resp.StatusCode)
	}
}

func TestAuthUpstreamFailure(t *testing.T) {
	ts, _, _, _ := newTestHTTP(t)
	resp, raw := authRequest(t, ts.Client(), "access-unavailable", http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "OAA 认证服务暂不可用") {
		t.Fatalf("body = %s", raw)
	}
}

func TestStateDoesNotLeakSecrets(t *testing.T) {
	ts, token, _, st, _ := newAuthedTestHTTP(t)
	view := createAccount(t, ts, token, "20240001")
	if err := st.SaveRememberCookies(testOwner, view.ID, "CASTGC=secret-cookie", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	resp, raw := authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	text := string(raw)
	for _, secret := range []string{"super-secret", "CASTGC=secret-cookie", `"passwordEnc"`, `"rememberCookiesEnc"`, testOAAPassword} {
		if strings.Contains(text, secret) {
			t.Fatalf("state leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "20240001") {
		t.Fatalf("state missing account: %s", text)
	}
	if !strings.Contains(text, `"campuses"`) || !strings.Contains(text, `"count":74`) {
		t.Fatalf("state missing campus catalog: %s", text)
	}
}

func TestAccountCRUDAndValidation(t *testing.T) {
	ts, token, _, st, _ := newAuthedTestHTTP(t)
	view := createAccount(t, ts, token, "20240002")
	if view.ID == 0 || view.Name != "测试账号" || !view.HasPassword {
		t.Fatalf("view = %+v", view)
	}

	resp, raw := authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/accounts", accountBody("20240002"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status = %d, body = %s", resp.StatusCode, raw)
	}

	bad := accountBody("20240003")
	bad["campus"] = "北京"
	resp, _ = authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/accounts", bad)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad campus status = %d", resp.StatusCode)
	}

	noPassword := accountBody("20240004")
	delete(noPassword, "password")
	resp, _ = authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/accounts", noPassword)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing password status = %d", resp.StatusCode)
	}

	upd := accountBody("20240002")
	upd["password"] = ""
	upd["name"] = "新名字"
	resp, raw = authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/accounts/1", upd)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", resp.StatusCode, raw)
	}
	acc, ok := st.Account(testOwner, view.ID)
	if !ok {
		t.Fatal("account missing")
	}
	if password, _ := st.Password(acc); password != "super-secret" {
		t.Fatalf("password = %q", password)
	}
	if acc.Name != "新名字" {
		t.Fatalf("name = %q", acc.Name)
	}

	resp, _ = authRequest(t, ts.Client(), token, http.MethodDelete, ts.URL+"/api/accounts/1", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodDelete, ts.URL+"/api/accounts/1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d", resp.StatusCode)
	}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/accounts/999", upd)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("update missing status = %d", resp.StatusCode)
	}
}

func TestScheduleAPI(t *testing.T) {
	ts, token, _, st, _ := newAuthedTestHTTP(t)
	view := createAccount(t, ts, token, "20240010")
	body := map[string]any{
		"enabled":              true,
		"timeOfDay":            "07:30",
		"accountIds":           []int64{view.ID, view.ID},
		"maxRetries":           2,
		"retryIntervalMinutes": 3,
	}
	resp, raw := authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/schedule", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("schedule status = %d, body = %s", resp.StatusCode, raw)
	}
	var saved store.Schedule
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.TimeOfDay != "07:30:00" || len(saved.AccountIDs) != 1 || !saved.Enabled {
		t.Fatalf("saved = %+v", saved)
	}
	if got := st.Schedule(testOwner); got.TimeOfDay != "07:30:00" || !got.Enabled {
		t.Fatalf("store schedule = %+v", got)
	}

	badTime := map[string]any{"enabled": false, "timeOfDay": "abc", "accountIds": []int64{}, "maxRetries": 1, "retryIntervalMinutes": 1}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/schedule", badTime)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad time status = %d", resp.StatusCode)
	}
	badAccount := map[string]any{"enabled": false, "timeOfDay": "07:00", "accountIds": []int64{999}, "maxRetries": 1, "retryIntervalMinutes": 1}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/schedule", badAccount)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad account status = %d", resp.StatusCode)
	}
	badRetries := map[string]any{"enabled": false, "timeOfDay": "07:00", "accountIds": []int64{}, "maxRetries": 11, "retryIntervalMinutes": 1}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/schedule", badRetries)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad retries status = %d", resp.StatusCode)
	}
}

func TestScheduleNotificationSettings(t *testing.T) {
	ts, token, _, st, _ := newAuthedTestHTTP(t)
	view := createAccount(t, ts, token, "20240012")
	body := map[string]any{
		"enabled":              true,
		"timeOfDay":            "07:30",
		"accountIds":           []int64{view.ID},
		"maxRetries":           2,
		"retryIntervalMinutes": 3,
		"notifyEnabled":        true,
		"notifyEmail":          " notify@example.com ",
	}
	resp, raw := authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/schedule", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("schedule status = %d, body = %s", resp.StatusCode, raw)
	}
	var saved store.Schedule
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.NotifyEnabled || saved.NotifyEmail != "notify@example.com" {
		t.Fatalf("saved notification = %+v", saved)
	}
	if got := st.Schedule(testOwner); !got.NotifyEnabled || got.NotifyEmail != "notify@example.com" {
		t.Fatalf("store notification = %+v", got)
	}

	resp, raw = authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state status = %d", resp.StatusCode)
	}
	var state stateResponse
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if !state.NotificationsAvailable {
		t.Fatal("notificationsAvailable = false")
	}
	if !state.Schedule.NotifyEnabled || state.Schedule.NotifyEmail != "notify@example.com" {
		t.Fatalf("state schedule = %+v", state.Schedule)
	}

	for _, email := range []string{"", "not-an-email", "a@b" + strings.Repeat("c", 300)} {
		body["notifyEmail"] = email
		resp, raw = authRequest(t, ts.Client(), token, http.MethodPut, ts.URL+"/api/schedule", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("email %q status = %d, body = %s", email, resp.StatusCode, raw)
		}
	}

	ts2, token2, _, _, _ := newAuthedTestHTTPWithNotify(t, false)
	unavailable := map[string]any{
		"enabled":              false,
		"timeOfDay":            "07:00",
		"accountIds":           []int64{},
		"maxRetries":           1,
		"retryIntervalMinutes": 1,
		"notifyEnabled":        true,
		"notifyEmail":          "notify@example.com",
	}
	resp, raw = authRequest(t, ts2.Client(), token2, http.MethodPut, ts2.URL+"/api/schedule", unavailable)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "SMTP") {
		t.Fatalf("unavailable status = %d, body = %s", resp.StatusCode, raw)
	}
	unavailable["notifyEnabled"] = false
	resp, _ = authRequest(t, ts2.Client(), token2, http.MethodPut, ts2.URL+"/api/schedule", unavailable)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disabled notification status = %d", resp.StatusCode)
	}
}

func TestRunAPIAndAsyncFailure(t *testing.T) {
	ts, token, _, st, _ := newAuthedTestHTTP(t)
	view := createAccount(t, ts, token, "20240020")
	if _, err := st.SaveSchedule(testOwner, store.Schedule{
		TimeOfDay:            "07:00:00",
		AccountIDs:           []int64{view.ID},
		MaxRetries:           0,
		RetryIntervalMinutes: 1,
	}); err != nil {
		t.Fatal(err)
	}

	resp, _ := authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/run", map[string]any{"accountIds": []int64{}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty run status = %d", resp.StatusCode)
	}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/run", map[string]any{"accountIds": []int64{999}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown run status = %d", resp.StatusCode)
	}
	resp, raw := authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/run", map[string]any{"accountIds": []int64{view.ID}})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("run status = %d, body = %s", resp.StatusCode, raw)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, stateRaw := authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/state", nil)
		var state stateResponse
		if err := json.Unmarshal(stateRaw, &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Runs) > 0 {
			if state.Progress.Running {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if state.Runs[0].Results[0].Status != checkin.StatusLoginFailed {
				t.Fatalf("run result = %+v", state.Runs[0].Results[0])
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("async run did not finish")
}

func TestHostAndContentTypeGuards(t *testing.T) {
	ts, token, _, _, _ := newAuthedTestHTTP(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/healthz", nil)
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("evil host status = %d", resp.StatusCode)
	}

	resp2, _ := jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/api/healthz", nil)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp2.StatusCode)
	}

	raw, _ := json.Marshal(accountBody("20240030"))
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/accounts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain status = %d", resp.StatusCode)
	}

	unknown := accountBody("20240031")
	unknown["unexpected"] = true
	resp3, _ := authRequest(t, ts.Client(), token, http.MethodPost, ts.URL+"/api/accounts", unknown)
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", resp3.StatusCode)
	}
}

func TestSampleServingAndTraversal(t *testing.T) {
	ts, token, _, _, dir := newAuthedTestHTTP(t)
	dateDir := filepath.Join(dir, "captchas", "2026-10-26")
	if err := os.MkdirAll(dateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("\xff\xd8\xff\xd9-test-sample")
	if err := os.WriteFile(filepath.Join(dateDir, "1-test.jpg"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	resp, raw := authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/samples/2026-10-26/1-test.jpg", nil)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(raw, content) {
		t.Fatalf("sample status=%d body=%q", resp.StatusCode, raw)
	}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/samples/2026-10-26/missing.jpg", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing sample status = %d", resp.StatusCode)
	}
	resp, _ = authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/samples/2026-10-26/%2e%2e%2fstate.json", nil)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("path traversal served a file")
	}
}

func TestStaticAssetsAndHeaders(t *testing.T) {
	ts, _, _, _ := newTestHTTP(t)
	resp, raw := jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "定时打卡") {
		t.Fatalf("index status=%d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("index content-type = %q", ct)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("csp = %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}

	resp, _ = jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/static/js/app.js", nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("app.js status=%d ct=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, _ = jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/static/js/views/accounts.js", nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("accounts.js status=%d ct=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, _ = jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/static/css/base.css", nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("base.css status=%d ct=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, _ = jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/static/css/", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("static directory status = %d", resp.StatusCode)
	}
	resp, _ = jsonRequest(t, ts.Client(), http.MethodGet, ts.URL+"/favicon.ico", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("favicon status = %d", resp.StatusCode)
	}
}

func TestCaptchaPreviewRouteRemoved(t *testing.T) {
	ts, token, _, _, _ := newAuthedTestHTTP(t)
	resp, raw := authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/captcha/preview", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("preview status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestCrossUserIsolation(t *testing.T) {
	ts, token1, _, _, _ := newAuthedTestHTTP(t)
	acc := createAccount(t, ts, token1, "20240101")

	resp, raw := authRequest(t, ts.Client(), token1, http.MethodPut, ts.URL+"/api/schedule", map[string]any{
		"enabled":              true,
		"timeOfDay":            "07:30:00",
		"accountIds":           []int64{acc.ID},
		"maxRetries":           1,
		"retryIntervalMinutes": 1,
		"notifyEnabled":        false,
		"notifyEmail":          "",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save schedule status = %d, body = %s", resp.StatusCode, raw)
	}

	token2 := loginAs(t, ts, testOtherOAAAccount, testOtherOAAPassword).Token
	resp, raw = authRequest(t, ts.Client(), token2, http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state status = %d, body = %s", resp.StatusCode, raw)
	}
	var other stateResponse
	if err := json.Unmarshal(raw, &other); err != nil {
		t.Fatal(err)
	}
	if len(other.Accounts) != 0 || len(other.Runs) != 0 || other.Schedule.Enabled {
		t.Fatalf("owner 2 sees owner 1 data: %d accounts, %d runs, schedule=%+v", len(other.Accounts), len(other.Runs), other.Schedule)
	}
	if other.Scheduler.Enabled || other.Scheduler.LastResult != "" {
		t.Fatalf("owner 2 scheduler status = %+v", other.Scheduler)
	}

	accountURL := ts.URL + "/api/accounts/" + strconv.FormatInt(acc.ID, 10)
	resp, _ = authRequest(t, ts.Client(), token2, http.MethodPut, accountURL, accountBody("20240101"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-owner update status = %d", resp.StatusCode)
	}
	resp, _ = authRequest(t, ts.Client(), token2, http.MethodDelete, accountURL, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-owner delete status = %d", resp.StatusCode)
	}
	resp, _ = authRequest(t, ts.Client(), token2, http.MethodPost, ts.URL+"/api/run", map[string]any{
		"accountIds": []int64{acc.ID},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-owner run status = %d", resp.StatusCode)
	}
	resp, _ = authRequest(t, ts.Client(), token2, http.MethodPut, ts.URL+"/api/schedule", map[string]any{
		"enabled":              true,
		"timeOfDay":            "07:30:00",
		"accountIds":           []int64{acc.ID},
		"maxRetries":           1,
		"retryIntervalMinutes": 1,
		"notifyEnabled":        false,
		"notifyEmail":          "",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cross-owner schedule status = %d", resp.StatusCode)
	}

	resp, raw = authRequest(t, ts.Client(), token1, http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner 1 state status = %d", resp.StatusCode)
	}
	var mine stateResponse
	if err := json.Unmarshal(raw, &mine); err != nil {
		t.Fatal(err)
	}
	if len(mine.Accounts) != 1 || !mine.Schedule.Enabled || len(mine.Schedule.AccountIDs) != 1 {
		t.Fatalf("owner 1 data changed: %d accounts, schedule=%+v", len(mine.Accounts), mine.Schedule)
	}
}

const legacyStateJSON = `{
  "version": 1,
  "accounts": [
    {"id": 1, "studentId": "24100999", "name": "迁移账号", "loginType": "password", "passwordEnc": "", "campus": "宜宾", "lastStatus": "成功：签到成功", "createdAt": "2026-10-26T10:00:00Z", "updatedAt": "2026-10-26T10:00:00Z"}
  ],
  "schedule": {"enabled": true, "timeOfDay": "19:01:00", "accountIds": [1], "maxRetries": 2, "retryIntervalMinutes": 5, "notifyEnabled": false, "lastRunDate": "2026-10-26", "lastResult": "成功 1，失败 0，共 1 个账号"},
  "runs": [
    {"id": 1, "trigger": "scheduled", "startedAt": "2026-10-26T11:01:00Z", "finishedAt": "2026-10-26T11:01:30Z", "summary": "成功 1，失败 0，共 1 个账号", "results": [{"accountId": 1, "studentId": "24100999", "accountName": "迁移账号", "status": "success", "message": "签到成功", "startedAt": "2026-10-26T11:01:00Z", "finishedAt": "2026-10-26T11:01:30Z"}]}
  ]
}`

func TestLegacyStateClaimedByFirstLogin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacyStateJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _, _ := buildTestServer(t, dir, true)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	token := loginAs(t, ts, testOAAAccount, testOAAPassword).Token
	resp, raw := authRequest(t, ts.Client(), token, http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state status = %d, body = %s", resp.StatusCode, raw)
	}
	var state stateResponse
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Accounts) != 1 || state.Accounts[0].StudentID != "24100999" {
		t.Fatalf("migrated accounts = %+v", state.Accounts)
	}
	if !state.Schedule.Enabled || state.Schedule.TimeOfDay != "19:01:00" || len(state.Schedule.AccountIDs) != 1 {
		t.Fatalf("migrated schedule = %+v", state.Schedule)
	}
	if len(state.Runs) != 1 || len(state.Runs[0].Results) != 1 {
		t.Fatalf("migrated runs = %+v", state.Runs)
	}

	other := loginAs(t, ts, testOtherOAAAccount, testOtherOAAPassword).Token
	resp, raw = authRequest(t, ts.Client(), other, http.MethodGet, ts.URL+"/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("other state status = %d", resp.StatusCode)
	}
	var otherState stateResponse
	if err := json.Unmarshal(raw, &otherState); err != nil {
		t.Fatal(err)
	}
	if len(otherState.Accounts) != 0 {
		t.Fatalf("second user inherited legacy data: %+v", otherState.Accounts)
	}
}
