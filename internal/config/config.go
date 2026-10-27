package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAddr     = "127.0.0.1:8099"
	DefaultDataDir  = "data"
	DefaultTimezone = "Asia/Shanghai"
	DefaultUIASBase = "https://uias.suse.edu.cn"
	DefaultQFHYBase = "https://qfhy.suse.edu.cn"
	DefaultOAABase  = "http://127.0.0.1:8080"
)

type Config struct {
	Addr        string
	DataDir     string
	Timezone    string
	UIASBase    string
	QFHYBase    string
	OAABase     string
	HTTPTimeout time.Duration
	SMTP        SMTPConfig
}

type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	From        string
	ImplicitTLS bool
	Timeout     time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Addr:        getenv("CHECKIN_ADDR", DefaultAddr),
		DataDir:     filepath.Clean(getenv("CHECKIN_DATA_DIR", DefaultDataDir)),
		Timezone:    getenv("CHECKIN_TZ", DefaultTimezone),
		UIASBase:    strings.TrimRight(getenv("CHECKIN_UIAS_BASE", DefaultUIASBase), "/"),
		QFHYBase:    strings.TrimRight(getenv("CHECKIN_QFHY_BASE", DefaultQFHYBase), "/"),
		OAABase:     strings.TrimRight(getenv("CHECKIN_OAA_BASE", DefaultOAABase), "/"),
		HTTPTimeout: 25 * time.Second,
	}
	smtpPort := 465
	if raw := strings.TrimSpace(os.Getenv("CHECKIN_SMTP_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("CHECKIN_SMTP_PORT 需在 1 到 65535 之间")
		}
		smtpPort = port
	}
	implicitTLS := true
	if raw := strings.TrimSpace(os.Getenv("CHECKIN_SMTP_TLS")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("CHECKIN_SMTP_TLS 必须是 true 或 false")
		}
		implicitTLS = value
	}
	cfg.SMTP = SMTPConfig{
		Host:        getenv("CHECKIN_SMTP_HOST", "smtp.qq.com"),
		Port:        smtpPort,
		Username:    strings.TrimSpace(os.Getenv("CHECKIN_SMTP_USER")),
		Password:    os.Getenv("CHECKIN_SMTP_PASS"),
		From:        strings.TrimSpace(os.Getenv("CHECKIN_SMTP_FROM")),
		ImplicitTLS: implicitTLS,
		Timeout:     20 * time.Second,
	}
	if raw := strings.TrimSpace(os.Getenv("CHECKIN_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("CHECKIN_TIMEOUT 不是合法时长: %w", err)
		}
		if d < time.Second || d > 2*time.Minute {
			return Config{}, fmt.Errorf("CHECKIN_TIMEOUT 需在 1s 到 2m 之间")
		}
		cfg.HTTPTimeout = d
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return Config{}, fmt.Errorf("监听地址不能为空")
	}
	if cfg.DataDir == "" || cfg.DataDir == "." {
		return Config{}, fmt.Errorf("数据目录不能为空")
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, fmt.Errorf("时区 %q 无效: %w", cfg.Timezone, err)
	}
	for name, raw := range map[string]string{"UIAS": cfg.UIASBase, "QFHY": cfg.QFHYBase, "OAA": cfg.OAABase} {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("%s 地址无效: %q", name, raw)
		}
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
