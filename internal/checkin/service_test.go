package checkin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"suse-checkin/internal/locations"
	"suse-checkin/internal/store"
	"suse-checkin/internal/upstream"
)

const testOwner uint64 = 1

type stubRecognizer struct {
	mu    sync.Mutex
	codes []string
	errs  []error
	calls int
}

func (r *stubRecognizer) Recognize(_ []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.calls
	r.calls++
	if i < len(r.errs) && r.errs[i] != nil {
		code := ""
		if i < len(r.codes) {
			code = r.codes[i]
		}
		return code, r.errs[i]
	}
	if i < len(r.codes) {
		return r.codes[i], nil
	}
	if len(r.codes) > 0 {
		return r.codes[len(r.codes)-1], nil
	}
	return "6800", nil
}

func (r *stubRecognizer) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakeOptions struct {
	mode          string
	needDate      string
	noTasks       bool
	detailStatus  int
	submitSuccess bool
	submitMessage string
}

func defaultFakeOptions() fakeOptions {
	return fakeOptions{
		mode:          "ok",
		needDate:      "2026-10-26",
		submitSuccess: true,
	}
}

type checkinFake struct {
	opts fakeOptions
	uias *httptest.Server
	qfhy *httptest.Server

	mu             sync.Mutex
	captchaFetches int
	loginPosts     int
	submitCalls    int
	lastSubmit     []byte
}

func newCheckinFake(t *testing.T, opts fakeOptions) *checkinFake {
	t.Helper()
	if opts.mode == "" {
		opts.mode = "ok"
	}
	if opts.needDate == "" {
		opts.needDate = "2026-10-26"
	}
	f := &checkinFake{opts: opts}

	qfhyMux := http.NewServeMux()
	qfhyMux.HandleFunc("/site/appware/system/sso/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "test-session", Path: "/"})
		http.Redirect(w, r, "/xg/app/qddk/admin/qddkdk", http.StatusFound)
	})
	qfhyMux.HandleFunc("/site/appware/system/sso/loginUrl", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("service"), "open_id=") {
			http.Error(w, "missing open_id", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "qr-session", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	qfhyMux.HandleFunc("/xg/app/qddk/admin/qddkdk", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>admin ok</html>")
	})
	qfhyMux.HandleFunc("/site/qddk/qdrw/api/myList.rst", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("SESSION"); err != nil {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		tasks := []map[string]any{}
		if !f.opts.noTasks {
			tasks = append(tasks,
				map[string]any{"id": 101, "rwmc": "今日任务", "rwzt": "进行中", "needTime": f.opts.needDate, "qdksrq": f.opts.needDate},
				map[string]any{"id": 100, "rwmc": "旧任务", "rwzt": "进行中", "needTime": "2026-10-25", "qdksrq": "2026-10-25"},
			)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resultCode": 0,
			"success":    true,
			"result":     map[string]any{"data": tasks, "total": len(tasks)},
		})
	})
	qfhyMux.HandleFunc("/site/qddk/qdrw/qdxx/api/detailList.rst", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("qdrwId"); got != "101" {
			http.Error(w, "bad task", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resultCode": 0,
			"success":    true,
			"result": map[string]any{"data": map[string]any{"dkxx": map[string]any{
				"id": 101, "qdzt": f.opts.detailStatus, "needTime": f.opts.needDate,
			}}},
		})
	})
	qfhyMux.HandleFunc("/site/qddk/qdrw/api/checkSignLocationWithPhoto.rst", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.submitCalls++
		f.lastSubmit = body
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resultCode": 0,
			"success":    f.opts.submitSuccess,
			"errorMsg":   f.opts.submitMessage,
			"result":     map[string]any{"data": f.opts.submitSuccess},
		})
	})
	f.qfhy = httptest.NewServer(qfhyMux)
	t.Cleanup(f.qfhy.Close)

	uiasMux := http.NewServeMux()
	uiasMux.HandleFunc("/sso/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if f.opts.mode == "remember" {
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
		f.loginPosts++
		f.mu.Unlock()
		switch f.opts.mode {
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
		f.mu.Lock()
		f.captchaFetches++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xd9})
	})
	f.uias = httptest.NewServer(uiasMux)
	t.Cleanup(f.uias.Close)
	return f
}

func (f *checkinFake) counts() (captchaFetches, loginPosts, submitCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.captchaFetches, f.loginPosts, f.submitCalls
}

func (f *checkinFake) submitted() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.lastSubmit...)
}

func newCheckinEnv(t *testing.T, fake *checkinFake, rec *stubRecognizer, maxRetries int) (*Service, *store.Store, int64, string) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	st, err := store.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := st.AddAccount(testOwner, store.AccountInput{
		StudentID: "20240001",
		Password:  "secret-pass",
		Name:      "测试账号",
		Campus:    "宜宾",
	})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 7, 0, 0, 0, loc)
	svc, err := New(Options{
		Store:        st,
		Client:       upstream.New(fake.uias.URL, fake.qfhy.URL, 2*time.Second),
		Recognizer:   rec,
		DataDir:      dataDir,
		Location:     loc,
		Now:          func() time.Time { return now },
		Sleep:        func(context.Context, time.Duration) error { return nil },
		AccountDelay: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSchedule(testOwner, store.Schedule{
		TimeOfDay:            "07:00:00",
		AccountIDs:           []int64{acc.ID},
		MaxRetries:           maxRetries,
		RetryIntervalMinutes: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return svc, st, acc.ID, dataDir
}

func checkinSopJWT(uid, name, openID string, exp time.Time) string {
	extra, _ := json.Marshal(map[string]string{"openId": openID, "userName": name})
	payload, _ := json.Marshal(map[string]any{
		"uid":   uid,
		"exp":   exp.Unix(),
		"extra": string(extra),
	})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestServiceWechatSessionCheckin(t *testing.T) {
	fake := newCheckinFake(t, defaultFakeOptions())
	rec := &stubRecognizer{}
	svc, st, _, _ := newCheckinEnv(t, fake, rec, 0)
	expires := time.Now().Add(48 * time.Hour)
	jwt := checkinSopJWT("20240030", "扫码用户", "open-1", time.Date(2026, 10, 28, 7, 0, 0, 0, time.UTC))
	view, err := st.AddWechatAccount(testOwner, store.WechatAccountInput{
		StudentID: "20240030",
		Name:      "扫码用户",
		Campus:    "宜宾",
		Cookies:   "_sop_session_=" + jwt + "; SESSION=old",
		ExpiresAt: expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{view.ID}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Results) != 1 {
		t.Fatalf("results = %d", len(run.Results))
	}
	res := run.Results[0]
	if res.Status != StatusSuccess || res.TaskID != 101 {
		t.Fatalf("result = %+v", res)
	}
	captchaFetches, loginPosts, submitCalls := fake.counts()
	if captchaFetches != 0 || loginPosts != 0 || submitCalls != 1 {
		t.Fatalf("counts = %d/%d/%d", captchaFetches, loginPosts, submitCalls)
	}
	acc, _ := st.Account(testOwner, view.ID)
	cookies, err := st.RememberCookies(acc)
	if err != nil || !strings.Contains(cookies, "SESSION=qr-session") {
		t.Fatalf("cookies = %q, %v", cookies, err)
	}
}

func TestServiceWechatSessionExpired(t *testing.T) {
	fake := newCheckinFake(t, defaultFakeOptions())
	rec := &stubRecognizer{}
	svc, st, _, _ := newCheckinEnv(t, fake, rec, 0)
	jwt := checkinSopJWT("20240031", "过期用户", "open-2", time.Date(2026, 10, 25, 7, 0, 0, 0, time.UTC))
	view, err := st.AddWechatAccount(testOwner, store.WechatAccountInput{
		StudentID: "20240031",
		Name:      "过期用户",
		Campus:    "宜宾",
		Cookies:   "_sop_session_=" + jwt + "; SESSION=old",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{view.ID}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusSessionExpired || !strings.Contains(run.Results[0].Message, "过期") {
		t.Fatalf("result = %+v", run.Results[0])
	}
	captchaFetches, loginPosts, submitCalls := fake.counts()
	if captchaFetches != 0 || loginPosts != 0 || submitCalls != 0 {
		t.Fatalf("counts = %d/%d/%d", captchaFetches, loginPosts, submitCalls)
	}
}

func TestServiceSuccess(t *testing.T) {
	fake := newCheckinFake(t, defaultFakeOptions())
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, st, id, dataDir := newCheckinEnv(t, fake, rec, 0)

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatalf("RunAccounts: %v", err)
	}
	if len(run.Results) != 1 {
		t.Fatalf("results = %d", len(run.Results))
	}
	res := run.Results[0]
	if res.Status != StatusSuccess || res.TaskID != 101 || res.OCRText != "6800" {
		t.Fatalf("result = %+v", res)
	}
	if res.CaptchaSample == "" {
		t.Fatal("captcha sample path is empty")
	}
	imagePath := filepath.Join(dataDir, filepath.FromSlash(res.CaptchaSample))
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("sample image: %v", err)
	}
	metaPath := strings.TrimSuffix(imagePath, filepath.Ext(imagePath)) + ".json"
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("sample meta: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["status"] != StatusSuccess || meta["ocrText"] != "6800" {
		t.Fatalf("meta = %+v", meta)
	}
	if !strings.Contains(run.Summary, "成功 1") {
		t.Fatalf("summary = %q", run.Summary)
	}

	captchaFetches, loginPosts, submitCalls := fake.counts()
	if captchaFetches != 1 || loginPosts != 1 || submitCalls != 1 {
		t.Fatalf("counts = %d/%d/%d", captchaFetches, loginPosts, submitCalls)
	}
	var payload map[string]any
	if err := json.Unmarshal(fake.submitted(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"].(float64) != 101 {
		t.Fatalf("submit payload = %+v", payload)
	}
	catalog, ok := locations.Find("宜宾")
	if !ok {
		t.Fatal("宜宾 catalog missing")
	}
	var matched *locations.Location
	for i := range catalog.Locations {
		loc := &catalog.Locations[i]
		if loc.Address == res.LocationAddress && loc.Latitude == res.LocationLatitude && loc.Longitude == res.LocationLongitude {
			matched = loc
			break
		}
	}
	if matched == nil {
		t.Fatalf("result location is not from 宜宾 catalog: %+v", res)
	}
	if payload["location"] != matched.LocationJSON {
		t.Fatalf("location JSON = %v, want %s", payload["location"], matched.LocationJSON)
	}
	if payload["qdddjtdz"] != matched.Address {
		t.Fatalf("qdddjtdz = %v, want %s", payload["qdddjtdz"], matched.Address)
	}

	snap := st.Snapshot(testOwner, 10)
	if len(snap.Runs) != 1 || snap.Runs[0].ID != run.ID {
		t.Fatalf("runs = %+v", snap.Runs)
	}
	if len(snap.Accounts) != 1 || !strings.Contains(snap.Accounts[0].LastStatus, "成功") {
		t.Fatalf("account = %+v", snap.Accounts)
	}
	if !snap.Accounts[0].HasRememberMe {
		t.Fatal("rememberMe cookies were not saved")
	}
}

func TestServiceAlreadyChecked(t *testing.T) {
	opts := defaultFakeOptions()
	opts.detailStatus = 1
	fake := newCheckinFake(t, opts)
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, _, id, _ := newCheckinEnv(t, fake, rec, 0)

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusAlreadyChecked {
		t.Fatalf("result = %+v", run.Results[0])
	}
	if _, _, submitCalls := fake.counts(); submitCalls != 0 {
		t.Fatalf("submit calls = %d", submitCalls)
	}
}

func TestServiceNoTask(t *testing.T) {
	opts := defaultFakeOptions()
	opts.noTasks = true
	fake := newCheckinFake(t, opts)
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, _, id, _ := newCheckinEnv(t, fake, rec, 0)

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusNoTask {
		t.Fatalf("result = %+v", run.Results[0])
	}
	if _, _, submitCalls := fake.counts(); submitCalls != 0 {
		t.Fatal("submitted without task")
	}
}

func TestServiceRetriesOCR(t *testing.T) {
	fake := newCheckinFake(t, defaultFakeOptions())
	rec := &stubRecognizer{
		codes: []string{"", "6800"},
		errs:  []error{errors.New("OCR boom"), nil},
	}
	svc, _, id, dataDir := newCheckinEnv(t, fake, rec, 1)

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusSuccess || rec.Calls() != 2 {
		t.Fatalf("result = %+v, calls = %d", run.Results[0], rec.Calls())
	}
	if captchaFetches, loginPosts, _ := fake.counts(); captchaFetches != 2 || loginPosts != 1 {
		t.Fatalf("captcha/login = %d/%d", captchaFetches, loginPosts)
	}
	files, err := filepath.Glob(filepath.Join(dataDir, "captchas", "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("sample metas = %d, want 2", len(files))
	}
}

func TestServiceSMSRequiredStopsRetry(t *testing.T) {
	opts := defaultFakeOptions()
	opts.mode = "sms"
	fake := newCheckinFake(t, opts)
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, _, id, _ := newCheckinEnv(t, fake, rec, 3)

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusSMSRequired {
		t.Fatalf("result = %+v", run.Results[0])
	}
	if _, loginPosts, _ := fake.counts(); loginPosts != 1 {
		t.Fatalf("login posts = %d, want 1", loginPosts)
	}
}

func TestServiceWrongPasswordStopsRetry(t *testing.T) {
	opts := defaultFakeOptions()
	opts.mode = "credentials"
	fake := newCheckinFake(t, opts)
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, _, id, _ := newCheckinEnv(t, fake, rec, 3)

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusLoginFailed {
		t.Fatalf("result = %+v", run.Results[0])
	}
	if _, loginPosts, _ := fake.counts(); loginPosts != 1 {
		t.Fatalf("login posts = %d, want 1", loginPosts)
	}
}

func TestServiceRememberMeSkipsCaptcha(t *testing.T) {
	opts := defaultFakeOptions()
	opts.mode = "remember"
	fake := newCheckinFake(t, opts)
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, st, id, _ := newCheckinEnv(t, fake, rec, 0)
	if err := st.SaveRememberCookies(testOwner, id, "CASTGC=valid", time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusSuccess {
		t.Fatalf("result = %+v", run.Results[0])
	}
	captchaFetches, loginPosts, submitCalls := fake.counts()
	if captchaFetches != 0 || loginPosts != 0 || submitCalls != 1 {
		t.Fatalf("counts = %d/%d/%d", captchaFetches, loginPosts, submitCalls)
	}
	if rec.Calls() != 0 {
		t.Fatalf("recognizer calls = %d", rec.Calls())
	}
}

func TestServiceSubmitBusinessFailure(t *testing.T) {
	opts := defaultFakeOptions()
	opts.submitSuccess = false
	opts.submitMessage = "超出签到范围"
	fake := newCheckinFake(t, opts)
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, _, id, _ := newCheckinEnv(t, fake, rec, 3)

	run, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[0].Status != StatusCheckinFailed || !strings.Contains(run.Results[0].Message, "超出签到范围") {
		t.Fatalf("result = %+v", run.Results[0])
	}
	if _, loginPosts, submitCalls := fake.counts(); loginPosts != 1 || submitCalls != 1 {
		t.Fatalf("login/submit = %d/%d", loginPosts, submitCalls)
	}
}

func TestPickTask(t *testing.T) {
	tasks := []upstream.Task{
		{ID: 1, Name: "old", Status: "进行中", NeedTime: "2026-10-25"},
		{ID: 2, Name: "today", Status: "进行中", NeedTime: "2026-10-26"},
	}
	if got := pickTask(tasks, "2026-10-26"); got.ID != 2 {
		t.Fatalf("picked %+v", got)
	}
	if got := pickTask(tasks, "2026-10-30"); got.ID != 1 {
		t.Fatalf("fallback picked %+v", got)
	}
	byStart := []upstream.Task{{ID: 3, Name: "start", Status: "进行中", StartDate: "2026-10-26"}}
	if got := pickTask(byStart, "2026-10-26"); got.ID != 3 {
		t.Fatalf("start date picked %+v", got)
	}
}

func TestSummarize(t *testing.T) {
	got := summarize([]store.AccountResult{
		{Status: StatusSuccess},
		{Status: StatusAlreadyChecked},
		{Status: StatusNoTask},
		{Status: StatusLoginFailed},
	})
	if got != "成功 3，失败 1，共 4 个账号" {
		t.Fatalf("summary = %q", got)
	}
}

func TestSampleMetaContainsNoSecrets(t *testing.T) {
	fake := newCheckinFake(t, defaultFakeOptions())
	rec := &stubRecognizer{codes: []string{"6800"}}
	svc, _, id, dataDir := newCheckinEnv(t, fake, rec, 0)
	if _, err := svc.RunAccounts(context.Background(), testOwner, []int64{id}, "manual"); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dataDir, "captchas", "*", "*.json"))
	if len(files) != 1 {
		t.Fatalf("meta files = %d", len(files))
	}
	raw, _ := os.ReadFile(files[0])
	if strings.Contains(string(raw), "secret-pass") || strings.Contains(string(raw), "CASTGC") {
		t.Fatalf("sample meta leaked secrets: %s", raw)
	}
}
