package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeUpstream struct {
	t      *testing.T
	uias   *httptest.Server
	qfhy   *httptest.Server
	mu     sync.Mutex
	mode   string
	lastPW string
	lastAC string
	lastEX string
	submit []byte
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{t: t, mode: "ok"}
	qfhyMux := http.NewServeMux()
	qfhyMux.HandleFunc("/site/appware/system/sso/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "test-session", Path: "/"})
		http.Redirect(w, r, "/xg/app/qddk/admin/qddkdk", http.StatusFound)
	})
	qfhyMux.HandleFunc("/xg/app/qddk/admin/qddkdk", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>admin ok</html>")
	})
	qfhyMux.HandleFunc("/site/qddk/qdrw/api/myList.rst", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("SESSION"); err != nil {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"resultCode":0,"success":true,"result":{"data":[{"id":101,"rwmc":"今日任务","rwzt":"进行中","needTime":"2026-10-26","qdksrq":"2026-10-26"},{"id":100,"rwmc":"旧任务","rwzt":"进行中","needTime":"2026-10-25","qdksrq":"2026-10-25"}],"total":2}}`)
	})
	qfhyMux.HandleFunc("/site/qddk/qdrw/qdxx/api/detailList.rst", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("qdrwId"); got != "101" {
			http.Error(w, "bad task", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"resultCode":0,"success":true,"result":{"data":{"dkxx":{"id":101,"qdzt":0,"needTime":"2026-10-26"}}}}`)
	})
	qfhyMux.HandleFunc("/site/qddk/qdrw/api/checkSignLocationWithPhoto.rst", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.submit = body
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"resultCode":0,"success":true,"result":{"data":true}}`)
	})
	f.qfhy = httptest.NewServer(qfhyMux)
	t.Cleanup(f.qfhy.Close)

	uiasMux := http.NewServeMux()
	uiasMux.HandleFunc("/sso/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if f.getMode() == "remember" {
				if c, err := r.Cookie("CASTGC"); err == nil && c.Value == "valid" {
					http.Redirect(w, r, f.qfhy.URL+"/site/appware/system/sso/login?ticket=remember", http.StatusFound)
					return
				}
			}
			http.SetCookie(w, &http.Cookie{Name: "CASTGC", Value: "valid", Path: "/"})
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><body><form method="post"><input name="execution" value="exec-123"><input name="username"></form></body></html>`)
			return
		}
		_ = r.ParseForm()
		f.mu.Lock()
		f.lastPW = r.Form.Get("password")
		f.lastAC = r.Form.Get("authcode")
		f.lastEX = r.Form.Get("execution")
		f.mu.Unlock()
		switch f.getMode() {
		case "captcha_error":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><div class="error">验证码错误</div></html>`)
			return
		case "sms":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><input name="phone" value="138****0000"><form><input name="smsCode"></form></html>`)
			return
		case "credentials":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><div class="error">用户名或密码错误</div></html>`)
			return
		}
		http.Redirect(w, r, f.qfhy.URL+"/site/appware/system/sso/login?ticket=test-ticket", http.StatusFound)
	})
	uiasMux.HandleFunc("/sso/captcha.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	})
	f.uias = httptest.NewServer(uiasMux)
	t.Cleanup(f.uias.Close)
	return f
}

func (f *fakeUpstream) setMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *fakeUpstream) getMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *fakeUpstream) client() *Client {
	return New(f.uias.URL, f.qfhy.URL, 2*time.Second)
}

func TestPasswordLoginTaskAndSubmitFlow(t *testing.T) {
	f := newFakeUpstream(t)
	ctx := context.Background()
	attempt, err := f.client().BeginPasswordLogin(ctx)
	if err != nil {
		t.Fatalf("BeginPasswordLogin: %v", err)
	}
	if len(attempt.Captcha()) == 0 {
		t.Fatal("empty captcha")
	}
	sess, err := attempt.Submit(ctx, "20240001", "secret-pass", "6800")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(f.lastPW) != 256 || strings.Contains(f.lastPW, "secret-pass") {
		t.Fatalf("password was not raw-RSA encrypted: %q", f.lastPW)
	}
	if f.lastAC != "6800" || f.lastEX != "exec-123" {
		t.Fatalf("authcode=%q execution=%q", f.lastAC, f.lastEX)
	}
	if cookies := sess.RememberCookies(); !strings.Contains(cookies, "CASTGC=valid") {
		t.Fatalf("remember cookies = %q", cookies)
	}

	tasks, err := sess.PendingTasks(ctx)
	if err != nil {
		t.Fatalf("PendingTasks: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != 101 || tasks[0].Name != "今日任务" {
		t.Fatalf("tasks = %+v", tasks)
	}
	detail, err := sess.TaskDetail(ctx, 101)
	if err != nil {
		t.Fatalf("TaskDetail: %v", err)
	}
	if detail.CheckStatus != 0 {
		t.Fatalf("detail = %+v", detail)
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	signTime := time.Date(2026, 10, 26, 7, 0, 0, 0, loc)
	resp, err := sess.SubmitCheckin(ctx, SignRequest{
		TaskID:    101,
		SignTime:  signTime,
		Address:   "四川省宜宾市翠屏区大学路",
		Latitude:  28.804867,
		Longitude: 104.674665,
	})
	if err != nil {
		t.Fatalf("SubmitCheckin: %v", err)
	}
	if !resp.Success || resp.ResultCode != 0 {
		t.Fatalf("submit response = %+v", resp)
	}
	var payload map[string]any
	if err := json.Unmarshal(f.submit, &payload); err != nil {
		t.Fatalf("submit body: %v (%s)", err, f.submit)
	}
	if payload["id"].(float64) != 101 || payload["qdzt"].(float64) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload["qdsj"] != "2026-10-26 07:00:00" {
		t.Fatalf("qdsj = %v", payload["qdsj"])
	}
	var location struct {
		Point   []float64 `json:"point"`
		Address string    `json:"address"`
	}
	if err := json.Unmarshal([]byte(payload["location"].(string)), &location); err != nil {
		t.Fatalf("location json: %v", err)
	}
	if len(location.Point) != 2 || location.Point[0] != 104.674665 || location.Point[1] != 28.804867 {
		t.Fatalf("location point = %+v", location.Point)
	}
	if payload["qdddjtdz"] != "四川省宜宾市翠屏区大学路" || payload["txxx"] != "{}" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestLoginErrorClassification(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		mode      string
		kind      LoginErrorKind
		retryable bool
	}{
		{mode: "captcha_error", kind: KindCaptcha, retryable: true},
		{mode: "sms", kind: KindSMSRequired, retryable: false},
		{mode: "credentials", kind: KindCredentials, retryable: false},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			f := newFakeUpstream(t)
			f.setMode(tc.mode)
			attempt, err := f.client().BeginPasswordLogin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = attempt.Submit(ctx, "20240001", "secret", "6800")
			var le *LoginError
			if !errors.As(err, &le) {
				t.Fatalf("error = %v, want LoginError", err)
			}
			if le.Kind != tc.kind || le.Retryable() != tc.retryable {
				t.Fatalf("kind=%s retryable=%v, want %s/%v", le.Kind, le.Retryable(), tc.kind, tc.retryable)
			}
		})
	}
}

func TestRememberLogin(t *testing.T) {
	f := newFakeUpstream(t)
	f.setMode("remember")
	ctx := context.Background()
	sess, ok, err := f.client().TryRememberLogin(ctx, "CASTGC=valid; JSESSIONID=abc")
	if err != nil {
		t.Fatalf("TryRememberLogin: %v", err)
	}
	if !ok || sess == nil {
		t.Fatalf("ok=%v sess=%v", ok, sess)
	}
	if _, err := sess.PendingTasks(ctx); err != nil {
		t.Fatalf("pending tasks after rememberMe: %v", err)
	}

	sess, ok, err = f.client().TryRememberLogin(ctx, "JSESSIONID=abc")
	if err != nil || ok || sess != nil {
		t.Fatalf("without CASTGC: sess=%v ok=%v err=%v", sess, ok, err)
	}
}

func TestParseExecutionAndLoginFailure(t *testing.T) {
	pages := []string{
		`<input name="execution" value="abc_1">`,
		`<input value="abc_2" name="execution">`,
		`<input type="hidden" name='execution' value='abc_3'>`,
	}
	for i, page := range pages {
		if got := parseExecution(page); got == "" {
			t.Fatalf("case %d: execution not found", i)
		}
	}
	if got := parseExecution(`<html>no token</html>`); got != "" {
		t.Fatalf("unexpected execution = %q", got)
	}
	if !requiresSMS(`<input name="smsCode">`) || !requiresSMS(`请填写短信验证码`) {
		t.Fatal("SMS page not detected")
	}
	normalLoginPage := `<form id="fm1"><input name="username"><input name="password" type="hidden"></form>` +
		`<form id="fm2"><input name="smscode"></form><span>短信登录</span><img src="/sso/captcha.jpg">`
	if requiresSMS(normalLoginPage) {
		t.Fatal("normal login page misdetected as SMS challenge")
	}
	if !requiresSMS(`<div>短信二次验证</div><input name="smscode">`) {
		t.Fatal("SMS challenge page not detected")
	}
	if !requiresSMS(`<form id="fm1"><input name="password"></form><div>sendsms_double</div>`) {
		t.Fatal("explicit sendsms_double not detected")
	}
	if phone := phoneMasked(`<input name="phone" value="138****0000">`); phone != "138****0000" {
		t.Fatalf("phone = %q", phone)
	}
	for _, tc := range []struct {
		page string
		kind LoginErrorKind
		want string
	}{
		{`<div class="alert error">验证码错误</div>`, KindCaptcha, "验证码"},
		{`<div class="form-error"><span class="form-tab-nav"></span>用户名或密码错误</div>`, KindCredentials, "密码"},
		{`<span id="msg1">密码错误</span>`, KindCredentials, "密码"},
		{`<form id="fm1"><input name="password"></form><div class="form-error"><span id="msg1" name="error_fm1" class="form-tab-nav">账号或密码错误。</span></div><input name="smscode">`, KindCredentials, "账号或密码错误"},
		{`<div class="form-error">请完成滑动验证</div>`, KindUnknown, "滑动"},
		{normalLoginPage, KindUnknown, "登录失败"},
	} {
		kind, msg := classifyLoginFailure(tc.page, 200)
		if kind != tc.kind || !strings.Contains(msg, tc.want) {
			t.Fatalf("classify(%q) = %s %q, want %s %q", tc.page, kind, msg, tc.kind, tc.want)
		}
	}
}

func TestBuildSignBodyUsesExactLocationJSON(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 10, 26, 7, 0, 0, 0, loc)
	locationJSON := `{"point": [104.674665, 28.804867], "address": "四川省宜宾市翠屏区大学路四川轻化工大学宜宾校区"}`
	body, err := BuildSignBody(SignRequest{TaskID: 7, SignTime: now, LocationJSON: locationJSON})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["location"] != locationJSON {
		t.Fatalf("location = %v", payload["location"])
	}
	if payload["qdddjtdz"] != "四川省宜宾市翠屏区大学路四川轻化工大学宜宾校区" {
		t.Fatalf("qdddjtdz = %v", payload["qdddjtdz"])
	}

	for _, bad := range []string{
		`{`,
		`{"address":"x"}`,
		`{"point":[0,0],"address":"x"}`,
		`{"point":[200,100],"address":"x"}`,
	} {
		if _, err := BuildSignBody(SignRequest{TaskID: 1, SignTime: now, LocationJSON: bad}); err == nil {
			t.Fatalf("bad location JSON accepted: %s", bad)
		}
	}
}

func TestBuildSignBodyValidation(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 10, 26, 7, 0, 0, 0, loc)
	good := SignRequest{TaskID: 1, SignTime: now, Address: "x", Latitude: 28.8, Longitude: 104.6}
	if _, err := BuildSignBody(good); err != nil {
		t.Fatal(err)
	}
	bad := []SignRequest{
		{TaskID: 0, SignTime: now, Address: "x", Latitude: 28.8, Longitude: 104.6},
		{TaskID: 1, SignTime: now, Address: "", Latitude: 28.8, Longitude: 104.6},
		{TaskID: 1, SignTime: now, Address: "x", Latitude: 0, Longitude: 0},
		{TaskID: 1, SignTime: time.Time{}, Address: "x", Latitude: 28.8, Longitude: 104.6},
		{TaskID: 1, SignTime: now, Address: "x", Latitude: 91, Longitude: 104.6},
	}
	for i, req := range bad {
		if _, err := BuildSignBody(req); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}
