package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"suse-checkin/internal/store"
)

var statusLabels = map[string]string{
	"success":          "签到成功",
	"already_checked":  "已签到",
	"no_task":          "无待签到任务",
	"captcha_failed":   "验证码失败",
	"login_failed":     "登录失败",
	"session_expired":  "扫码会话已过期",
	"sms_required":     "需要短信验证",
	"checkin_failed":   "签到失败",
	"location_missing": "缺少签到位置",
	"error":            "异常",
}

type Config struct {
	Host        string
	Port        int
	Username    string
	Password    string
	From        string
	ImplicitTLS bool
	Timeout     time.Duration
}

type Sender struct {
	cfg     Config
	from    string
	enabled bool
	logger  *log.Logger
}

func New(cfg Config, logger *log.Logger) (*Sender, error) {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.From = strings.TrimSpace(cfg.From)
	if cfg.Username == "" && cfg.Password == "" {
		return &Sender{cfg: cfg, logger: logger}, nil
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("SMTP 用户名和密码必须同时配置")
	}
	if cfg.Host == "" {
		return nil, errors.New("SMTP 服务器地址不能为空")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("SMTP 端口需在 1 到 65535 之间")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.From == "" {
		cfg.From = cfg.Username
	}
	addr, err := mail.ParseAddress(cfg.From)
	if err != nil || addr.Address == "" {
		return nil, fmt.Errorf("发件邮箱无效")
	}
	return &Sender{
		cfg:     cfg,
		from:    addr.Address,
		enabled: true,
		logger:  logger,
	}, nil
}

func (s *Sender) Available() bool {
	return s != nil && s.enabled
}

func (s *Sender) NotifyFailure(ctx context.Context, to string, run store.Run, maxRetries int) error {
	if !s.Available() {
		return errors.New("邮件通知未配置")
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(to))
	if err != nil || addr.Address == "" {
		return errors.New("通知邮箱格式不正确")
	}
	subject, body := FailureEmail(run, maxRetries)
	if subject == "" {
		return nil
	}
	if err := s.send(ctx, addr.Address, subject, body); err != nil {
		return fmt.Errorf("发送失败通知: %w", err)
	}
	s.logger.Printf("[notify] 打卡失败通知已发送")
	return nil
}

// FailureEmail 只包含失败账号的脱敏摘要，不包含密码、Cookie 或完整请求。
func FailureEmail(run store.Run, maxRetries int) (subject, body string) {
	failures := failedResults(run.Results)
	if len(failures) == 0 {
		return "", ""
	}
	executedAt := run.StartedAt
	if executedAt.IsZero() {
		executedAt = time.Now()
	}
	subject = fmt.Sprintf("【SUSE 定时打卡】打卡失败通知 · %s", executedAt.Format("2006-01-02"))
	success := len(run.Results) - len(failures)
	attempts := maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"zh-CN\"><body style=\"margin:0;background:#f2f6f2;padding:24px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC',sans-serif;color:#152521;\">")
	b.WriteString("<div style=\"max-width:640px;margin:0 auto;background:#fff;border:1px solid #e0e9e3;border-radius:18px;padding:24px;\">")
	b.WriteString("<h2 style=\"margin:0 0 6px;font-size:20px;\">定时打卡失败</h2>")
	b.WriteString("<p style=\"margin:0 0 16px;color:#6d7d77;font-size:13px;\">服务在线，但本次定时任务在重试后仍未全部成功。</p>")
	fmt.Fprintf(&b, "<p style=\"margin:0 0 4px;font-size:13px;\">执行时间：%s</p>", html.EscapeString(executedAt.Format("2006-01-02 15:04:05")))
	fmt.Fprintf(&b, "<p style=\"margin:0 0 4px;font-size:13px;\">尝试次数：最多 %d 次（失败重试 %d 次）</p>", attempts, maxRetries)
	fmt.Fprintf(&b, "<p style=\"margin:0 0 16px;font-size:13px;\">结果：成功 %d，失败 %d，共 %d 个账号</p>", success, len(failures), len(run.Results))
	b.WriteString("<table style=\"width:100%;border-collapse:collapse;font-size:13px;\"><thead><tr style=\"text-align:left;color:#6d7d77;\"><th style=\"padding:8px;border-bottom:1px solid #e0e9e3;\">账号</th><th style=\"padding:8px;border-bottom:1px solid #e0e9e3;\">状态</th><th style=\"padding:8px;border-bottom:1px solid #e0e9e3;\">原因</th></tr></thead><tbody>")
	for _, result := range failures {
		name := strings.TrimSpace(result.AccountName)
		if name == "" {
			name = result.StudentID
		}
		label := statusLabels[result.Status]
		if label == "" {
			label = result.Status
		}
		detail := strings.TrimSpace(result.Message)
		if result.TaskName != "" {
			detail = strings.TrimSpace(detail + "；任务：" + result.TaskName)
		}
		fmt.Fprintf(&b,
			"<tr><td style=\"padding:8px;border-bottom:1px solid #eff4f1;\">%s<br><span style=\"color:#6d7d77;font-size:12px;\">%s</span></td><td style=\"padding:8px;border-bottom:1px solid #eff4f1;color:#b4483c;\">%s</td><td style=\"padding:8px;border-bottom:1px solid #eff4f1;\">%s</td></tr>",
			html.EscapeString(name),
			html.EscapeString(result.StudentID),
			html.EscapeString(label),
			html.EscapeString(detail),
		)
	}
	b.WriteString("</tbody></table>")
	b.WriteString("<p style=\"margin:18px 0 0;color:#6d7d77;font-size:12px;\">请登录打卡页面查看运行记录和验证码样本。此邮件由系统自动发送，请勿回复。</p>")
	b.WriteString("</div></body></html>")
	return subject, b.String()
}

func failedResults(results []store.AccountResult) []store.AccountResult {
	out := make([]store.AccountResult, 0, len(results))
	for _, result := range results {
		switch result.Status {
		case "success", "already_checked", "no_task":
			continue
		default:
			out = append(out, result)
		}
	}
	return out
}

func (s *Sender) send(ctx context.Context, to, subject, body string) error {
	timeout := s.cfg.Timeout
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if !deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	tlsConfig := &tls.Config{
		ServerName: s.cfg.Host,
		MinVersion: tls.VersionTLS12,
	}
	if s.cfg.ImplicitTLS {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("SMTP TLS 握手: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("初始化 SMTP 会话: %w", err)
	}
	defer client.Close()

	if !s.cfg.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 服务器未提供 STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
		return fmt.Errorf("SMTP 登录: %w", err)
	}
	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("SMTP 发件人: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP 收件人: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP 开始正文: %w", err)
	}
	if _, err := writer.Write(buildMessage(s.from, to, subject, body)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("SMTP 写入正文: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("SMTP 结束正文: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("SMTP 结束会话: %w", err)
	}
	return nil
}

func buildMessage(from, to, subject, body string) []byte {
	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body)
}
