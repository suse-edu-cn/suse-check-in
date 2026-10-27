package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"suse-checkin/internal/store"
)

func TestNewDisabledAndValidation(t *testing.T) {
	sender, err := New(Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sender.Available() {
		t.Fatal("empty SMTP config should be disabled")
	}

	if _, err := New(Config{Username: "sender@example.com", Host: "smtp.example.com", Port: 465}, nil); err == nil {
		t.Fatal("partial SMTP config accepted")
	}
	if _, err := New(Config{Username: "sender@example.com", Password: "pass", Host: "", Port: 465}, nil); err == nil {
		t.Fatal("missing host accepted")
	}
	if _, err := New(Config{Username: "sender@example.com", Password: "pass", Host: "smtp.example.com", Port: 0}, nil); err == nil {
		t.Fatal("invalid port accepted")
	}
	if _, err := New(Config{Username: "sender@example.com", Password: "pass", Host: "smtp.example.com", Port: 465, From: "bad"}, nil); err == nil {
		t.Fatal("invalid from accepted")
	}

	sender, err = New(Config{
		Host:     "smtp.example.com",
		Port:     465,
		Username: "sender@example.com",
		Password: "pass",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sender.Available() || sender.from != "sender@example.com" {
		t.Fatalf("sender = %+v", sender)
	}
}

func TestFailureEmail(t *testing.T) {
	started := time.Date(2026, 10, 26, 7, 0, 5, 0, time.FixedZone("CST", 8*3600))
	run := store.Run{
		StartedAt: started,
		Results: []store.AccountResult{
			{AccountName: "成功账号", StudentID: "20240001", Status: "success"},
			{AccountName: "<b>失败账号</b>", StudentID: "20240002", Status: "login_failed", Message: "密码错误 <script>alert(1)</script>", TaskName: "晨检"},
		},
	}
	subject, body := FailureEmail(run, 2)
	if !strings.Contains(subject, "2026-10-26") {
		t.Fatalf("subject = %q", subject)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>失败账号</b>") {
		t.Fatalf("body contains unescaped values: %s", body)
	}
	for _, want := range []string{"&lt;b&gt;失败账号&lt;/b&gt;", "20240002", "登录失败", "密码错误", "晨检", "最多 3 次"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "成功账号") {
		t.Fatalf("body contains successful account: %s", body)
	}
}

func TestFailureEmailWithoutFailures(t *testing.T) {
	subject, body := FailureEmail(store.Run{
		Results: []store.AccountResult{{Status: "success"}, {Status: "no_task"}, {Status: "already_checked"}},
	}, 1)
	if subject != "" || body != "" {
		t.Fatalf("expected empty email, got %q / %q", subject, body)
	}
}

func TestNotifyFailureValidation(t *testing.T) {
	disabled, err := New(Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := disabled.NotifyFailure(context.Background(), "to@example.com", store.Run{}, 1); err == nil {
		t.Fatal("disabled sender sent email")
	}

	sender, err := New(Config{
		Host:     "127.0.0.1",
		Port:     1,
		Username: "sender@example.com",
		Password: "pass",
		Timeout:  200 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.NotifyFailure(context.Background(), "bad", store.Run{}, 1); err == nil || !strings.Contains(err.Error(), "通知邮箱") {
		t.Fatalf("invalid recipient error = %v", err)
	}
	if err := sender.NotifyFailure(context.Background(), "to@example.com", store.Run{}, 1); err != nil {
		t.Fatalf("empty result should be a no-op: %v", err)
	}
}

func TestNotifyFailureConnectionError(t *testing.T) {
	sender, err := New(Config{
		Host:     "127.0.0.1",
		Port:     1,
		Username: "sender@example.com",
		Password: "pass",
		Timeout:  300 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := store.Run{
		StartedAt: time.Now(),
		Results: []store.AccountResult{
			{AccountName: "失败账号", StudentID: "20240002", Status: "login_failed", Message: "密码错误"},
		},
	}
	if err := sender.NotifyFailure(context.Background(), "to@example.com", run, 1); err == nil {
		t.Fatal("expected SMTP connection failure")
	}
}

func TestBuildMessage(t *testing.T) {
	message := string(buildMessage("sender@example.com", "to@example.com", "【测试】打卡失败", "<p>body</p>"))
	for _, want := range []string{
		"From: sender@example.com\r\n",
		"To: to@example.com\r\n",
		"Subject: =?UTF-8?",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/html; charset=UTF-8\r\n",
		"\r\n\r\n<p>body</p>",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q: %s", want, message)
		}
	}
}
