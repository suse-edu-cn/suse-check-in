package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"suse-checkin/internal/captcha"
	"suse-checkin/internal/checkin"
	"suse-checkin/internal/config"
	"suse-checkin/internal/notify"
	"suse-checkin/internal/oaa"
	"suse-checkin/internal/scheduler"
	"suse-checkin/internal/server"
	"suse-checkin/internal/store"
	"suse-checkin/internal/upstream"
	"suse-checkin/internal/version"
	"suse-checkin/internal/wechat"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	addr := flag.String("addr", cfg.Addr, "HTTP 监听地址")
	captchaCheck := flag.Bool("captcha-check", false, "抓取一张真实验证码并识别，然后退出")
	showVersion := flag.Bool("version", false, "打印版本并退出")
	flag.Parse()
	cfg.Addr = *addr
	if *showVersion {
		fmt.Println(version.Version)
		return nil
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}
	if _, ok := os.LookupEnv("GOCAPTCHA_CACHE_DIR"); !ok {
		_ = os.Setenv("GOCAPTCHA_CACHE_DIR", filepath.Join(cfg.DataDir, "gocaptcha"))
	}

	logger := log.New(os.Stdout, "", log.LstdFlags)
	logger.Printf("suse-checkin %s", version.Version)
	logger.Printf("加载 GoCaptcha 模型...")
	recognizer, err := captcha.New()
	if err != nil {
		return fmt.Errorf("初始化 GoCaptcha: %w", err)
	}
	defer recognizer.Close()

	client := upstream.New(cfg.UIASBase, cfg.QFHYBase, cfg.HTTPTimeout)
	if *captchaCheck {
		return runCaptchaCheck(client, recognizer)
	}

	st, err := store.New(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.LoadLegacyError(); err != nil {
		logger.Printf("[store] 旧版数据不可用，已跳过迁移: %v", err)
	}
	if st.LegacyPending() {
		logger.Printf("[store] 检测到旧版 state.json：第一个登录的 OAA 账号将接手这些账号、定时和运行记录")
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("加载时区: %w", err)
	}
	svc, err := checkin.New(checkin.Options{
		Store:        st,
		Client:       client,
		Recognizer:   recognizer,
		DataDir:      cfg.DataDir,
		Location:     loc,
		Logger:       logger,
		AccountDelay: time.Second,
	})
	if err != nil {
		return err
	}
	notifier, err := notify.New(notify.Config{
		Host:        cfg.SMTP.Host,
		Port:        cfg.SMTP.Port,
		Username:    cfg.SMTP.Username,
		Password:    cfg.SMTP.Password,
		From:        cfg.SMTP.From,
		ImplicitTLS: cfg.SMTP.ImplicitTLS,
		Timeout:     cfg.SMTP.Timeout,
	}, logger)
	if err != nil {
		return fmt.Errorf("初始化邮件通知: %w", err)
	}
	sched := scheduler.New(st, svc, notifier, loc, logger)
	oaaClient, err := oaa.New(cfg.OAABase, cfg.HTTPTimeout)
	if err != nil {
		return fmt.Errorf("初始化 OAA 认证: %w", err)
	}
	wechatSvc := wechat.New(client, st, logger)
	defer wechatSvc.Close()
	srv, err := server.New(cfg, st, svc, sched, oaaClient, wechatSvc, notifier.Available(), logger)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.ListenAndServe()
	}()
	sched.Start()
	logger.Printf("OAA 认证服务: %s", cfg.OAABase)
	if notifier.Available() {
		logger.Printf("邮件失败通知已启用")
	} else {
		logger.Printf("邮件失败通知未配置（需要 CHECKIN_SMTP_USER 与 CHECKIN_SMTP_PASS）")
	}
	logger.Printf("服务已启动: http://%s（监听 %s）", displayAddr(cfg.Addr), cfg.Addr)
	if isWildcardAddr(cfg.Addr) {
		logger.Printf("提示: %s 对所有网卡开放；页面需 OAA 账号登录，请在反向代理层配置 HTTPS 与访问控制", cfg.Addr)
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			sched.Stop()
			return err
		}
	case <-ctx.Done():
		logger.Printf("收到退出信号，正在关闭...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	sched.Stop()
	logger.Printf("已退出")
	return nil
}

func runCaptchaCheck(client *upstream.Client, recognizer *captcha.GoCaptcha) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	attempt, err := client.BeginPasswordLogin(ctx)
	if err != nil {
		return fmt.Errorf("抓取真实验证码失败: %w", err)
	}
	start := time.Now()
	text, ocrErr := recognizer.Recognize(attempt.Captcha())
	elapsed := time.Since(start)
	fmt.Printf("验证码识别: %q (%d ms)\n", text, elapsed.Milliseconds())
	if ocrErr != nil {
		return fmt.Errorf("识别失败: %w", ocrErr)
	}
	return nil
}

// isWildcardAddr 判断监听地址是否对所有网卡开放（空主机名、0.0.0.0、::）。
func isWildcardAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	return host == "" || host == "0.0.0.0" || host == "::"
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}
