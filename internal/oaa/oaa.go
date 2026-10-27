package oaa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var ErrUnauthorized = errors.New("OAA 登录状态无效")

const (
	validateTTL   = 30 * time.Second
	maxBodyBytes  = 1 << 20
	maxCacheItems = 512
)

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

type Tokens struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
}

type Avatar struct {
	URL string `json:"url"`
	URI string `json:"uri"`
}

type User struct {
	UserID     uint64 `json:"user_id"`
	StudentID  string `json:"student_id"`
	Username   string `json:"username"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Department string `json:"department"`
	Role       string `json:"role"`
	RoleLevel  uint64 `json:"role_level"`
	Avatar     Avatar `json:"avatar"`
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type cacheEntry struct {
	user    User
	expires time.Time
}

type Client struct {
	base  string
	http  *http.Client
	mu    sync.Mutex
	cache map[[sha256.Size]byte]cacheEntry
}

func New(baseURL string, timeout time.Duration) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("OAA 地址无效: %q", baseURL)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		base: base,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache: make(map[[sha256.Size]byte]cacheEntry),
	}, nil
}

func (c *Client) Login(ctx context.Context, account, password, device string) (Tokens, error) {
	var out Tokens
	err := c.do(ctx, http.MethodPost, "/v2/auth/login", "", map[string]string{
		"account":  account,
		"password": password,
		"device":   device,
	}, &out)
	return out, err
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	var out Tokens
	err := c.do(ctx, http.MethodPost, "/v2/auth/refresh", "", map[string]string{
		"refresh_token": refreshToken,
	}, &out)
	return out, err
}

func (c *Client) Logout(ctx context.Context, accessToken, device string) error {
	return c.do(ctx, http.MethodPost, "/v2/auth/logout", accessToken, map[string]string{
		"device": device,
	}, nil)
}

func (c *Client) Me(ctx context.Context, accessToken string) (User, error) {
	var user User
	if err := c.do(ctx, http.MethodGet, "/v2/user/me", accessToken, nil, &user); err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			return User{}, fmt.Errorf("%w: %s", ErrUnauthorized, apiErr.Message)
		}
		return User{}, err
	}
	return user, nil
}

// Validate 使用 OAA 的 /v2/user/me 校验 Access Token，并做短时缓存，
// 避免本地页面轮询时频繁请求认证服务。
func (c *Client) Validate(ctx context.Context, accessToken string) (User, error) {
	if strings.TrimSpace(accessToken) == "" {
		return User{}, ErrUnauthorized
	}
	key := sha256.Sum256([]byte(accessToken))
	now := time.Now()

	c.mu.Lock()
	entry, ok := c.cache[key]
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.user, nil
	}

	user, err := c.Me(ctx, accessToken)
	if err != nil {
		return User{}, err
	}

	expires := now.Add(validateTTL)
	if exp, ok := TokenExpiry(accessToken); ok && exp.Before(expires) {
		expires = exp
	}
	if expires.After(now) {
		c.mu.Lock()
		if len(c.cache) >= maxCacheItems {
			for k, v := range c.cache {
				if !now.Before(v.expires) {
					delete(c.cache, k)
				}
			}
			if len(c.cache) >= maxCacheItems {
				c.cache = make(map[[sha256.Size]byte]cacheEntry)
			}
		}
		c.cache[key] = cacheEntry{user: user, expires: expires}
		c.mu.Unlock()
	}
	return user, nil
}

func (c *Client) Invalidate(accessToken string) {
	key := sha256.Sum256([]byte(accessToken))
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
}

func (c *Client) do(ctx context.Context, method, path, accessToken string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("编码 OAA 请求: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("创建 OAA 请求: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("请求 OAA 失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("读取 OAA 响应: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("OAA 返回无效响应（HTTP %d）", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || env.Code != http.StatusOK {
		message := strings.TrimSpace(env.Message)
		if message == "" {
			message = fmt.Sprintf("OAA 请求失败（HTTP %d）", resp.StatusCode)
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			status = env.Code
		}
		return &Error{Status: status, Message: message}
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("解析 OAA 响应数据: %w", err)
	}
	return nil
}

// TokenExpiry 只读取 JWT 的 exp 用于限制缓存时间，不做签名校验。
func TokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}
