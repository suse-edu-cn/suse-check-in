package upstream

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	wechatAppID            = "wx130c9f0196e29149"
	qfhyWechatClientIDPath = "/edu/v2/weixin/getClientId"
	qfhyWechatQRCodePath   = "/edu/v2/weixin/getQrCodeUrl"
	qfhyWechatStreamPath   = "/edu/v1/wechat/stream"
	qfhyWechatCallbackPath = "/callback/edu/"
	qfhySSOLoginURLPath    = "/site/appware/system/sso/loginUrl"
	qfhyCurrentUserPath    = "/site/app/base/common/api/user/current.rst"
	qfhyQRAdminPath        = "/xg/app/qddk/admin"
	wechatSessionDays      = 7
)

var (
	ErrWechatQRExpired      = errors.New("微信二维码已过期，请刷新重试")
	ErrWechatSessionExpired = errors.New("扫码会话已过期，请重新扫码登录")
	errWechatCancelled      = errors.New("微信扫码已取消")
)

type WechatLogin struct {
	client    *Client
	jar       http.CookieJar
	api       *http.Client
	clientID  string
	qrCode    string
	expiresAt time.Time
}

type WechatLoginResult struct {
	StudentID string
	Name      string
	OpenID    string
	Cookies   string
	ExpiresAt time.Time
}

type sopClaims struct {
	StudentID string
	Name      string
	OpenID    string
	ExpiresAt time.Time
}

func (w *WechatLogin) QRCode() string       { return w.qrCode }
func (w *WechatLogin) ExpiresAt() time.Time { return w.expiresAt }

func (c *Client) StartWechatLogin(ctx context.Context) (*WechatLogin, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("创建扫码 Cookie 容器: %w", err)
	}
	api := &http.Client{Timeout: c.timeout, Jar: jar}

	clientID, err := c.wechatClientID(ctx, api)
	if err != nil {
		return nil, err
	}
	qrCode, minutes, err := c.wechatQRCode(ctx, api, clientID)
	if err != nil {
		return nil, err
	}
	if minutes <= 0 {
		minutes = 5
	}
	return &WechatLogin{
		client:    c,
		jar:       jar,
		api:       api,
		clientID:  clientID,
		qrCode:    qrCode,
		expiresAt: time.Now().Add(time.Duration(minutes) * time.Minute),
	}, nil
}

func (c *Client) wechatClientID(ctx context.Context, api *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.qfhyBase+qfhyWechatClientIDPath, nil)
	if err != nil {
		return "", err
	}
	c.setWechatAPIHeaders(req)
	resp, err := api.Do(req)
	if err != nil {
		return "", fmt.Errorf("获取微信 ClientId: %w", err)
	}
	defer resp.Body.Close()
	raw, err := readBody(resp.Body, maxBody)
	if err != nil {
		return "", fmt.Errorf("读取微信 ClientId: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("微信 ClientId 接口返回 HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ClientID string `json:"client_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析微信 ClientId: %w", err)
	}
	if out.Code != http.StatusOK || out.Data.ClientID == "" {
		return "", fmt.Errorf("获取微信 ClientId 失败: %s", nonEmpty(out.Msg, "服务端返回为空"))
	}
	return out.Data.ClientID, nil
}

func (c *Client) wechatQRCode(ctx context.Context, api *http.Client, clientID string) (string, int, error) {
	payload, _ := json.Marshal(map[string]string{
		"app_id":    wechatAppID,
		"client_id": clientID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.qfhyBase+qfhyWechatQRCodePath, strings.NewReader(string(payload)))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setWechatAPIHeaders(req)
	resp, err := api.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("获取微信二维码: %w", err)
	}
	defer resp.Body.Close()
	raw, err := readBody(resp.Body, maxBody)
	if err != nil {
		return "", 0, fmt.Errorf("读取微信二维码: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("微信二维码接口返回 HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Img     string `json:"img"`
			ImgType string `json:"imgType"`
			Minute  int    `json:"minute"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, fmt.Errorf("解析微信二维码: %w", err)
	}
	if out.Code != http.StatusOK {
		return "", 0, fmt.Errorf("获取微信二维码失败: %s", nonEmpty(out.Msg, "服务端返回为空"))
	}
	image := strings.TrimSpace(out.Data.Img)
	if image == "" {
		image = strings.TrimSpace(out.Data.URL)
	}
	if image == "" {
		return "", 0, errors.New("微信二维码图片为空")
	}
	image, err = c.wechatQRImage(ctx, api, image)
	if err != nil {
		return "", 0, err
	}
	return image, out.Data.Minute, nil
}

func (c *Client) wechatQRImage(ctx context.Context, api *http.Client, image string) (string, error) {
	switch {
	case strings.HasPrefix(image, "data:image/"):
		return image, nil
	case strings.HasPrefix(image, "http://"), strings.HasPrefix(image, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, image, nil)
		if err != nil {
			return "", err
		}
		c.setCommonHeaders(req)
		resp, err := api.Do(req)
		if err != nil {
			return "", fmt.Errorf("下载微信二维码: %w", err)
		}
		defer resp.Body.Close()
		raw, err := readBody(resp.Body, maxCaptchaBody)
		if err != nil {
			return "", fmt.Errorf("读取微信二维码图片: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("微信二维码图片返回 HTTP %d", resp.StatusCode)
		}
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
			contentType = "image/png"
		}
		return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
	default:
		return "data:image/png;base64," + image, nil
	}
}

func (w *WechatLogin) Wait(ctx context.Context, onStatus func(int)) (WechatLoginResult, error) {
	waitCtx, cancel := context.WithDeadline(ctx, w.expiresAt)
	defer cancel()
	scanned := false
	retries := 0
	for {
		result, err := w.streamOnce(waitCtx, func(status int) {
			if status == 1 && !scanned {
				scanned = true
				if onStatus != nil {
					onStatus(1)
				}
			}
			if status == 2 && onStatus != nil {
				onStatus(2)
			}
		})
		if result != nil {
			return *result, nil
		}
		if errors.Is(err, errWechatCancelled) {
			return WechatLoginResult{}, err
		}
		if waitCtx.Err() != nil {
			return WechatLoginResult{}, ErrWechatQRExpired
		}
		if err == nil {
			err = io.EOF
		}
		if retries >= 3 {
			return WechatLoginResult{}, fmt.Errorf("微信扫码连接中断: %w", err)
		}
		retries++
		select {
		case <-waitCtx.Done():
			return WechatLoginResult{}, ErrWechatQRExpired
		case <-time.After(600 * time.Millisecond):
		}
	}
}

func (w *WechatLogin) streamOnce(ctx context.Context, onStatus func(int)) (*WechatLoginResult, error) {
	endpoint := w.client.qfhyBase + qfhyWechatStreamPath + "?clientId=" + url.QueryEscape(w.clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Origin", w.client.qfhyBase)
	req.Header.Set("Referer", w.client.qfhyBase+"/edu/v1/wechat/qrcodelogin")
	w.client.setCommonHeaders(req)
	resp, err := (&http.Client{Jar: w.jar}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := readBody(resp.Body, maxBody)
		return nil, fmt.Errorf("微信扫码状态接口返回 HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		parts := strings.SplitN(data, ",", 2)
		switch strings.TrimSpace(parts[0]) {
		case "1":
			onStatus(1)
		case "2":
			onStatus(2)
			if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
				return nil, errors.New("扫码成功但回调参数为空")
			}
			return w.complete(ctx, parts[1])
		default:
			return nil, errWechatCancelled
		}
	}
}

func (w *WechatLogin) complete(ctx context.Context, callbackQuery string) (*WechatLoginResult, error) {
	query := strings.TrimSpace(callbackQuery)
	query = strings.TrimPrefix(query, "?")
	query = strings.TrimPrefix(query, "&")
	callback := w.client.qfhyBase + qfhyWechatCallbackPath
	if strings.Contains(callback, "?") {
		callback += "&"
	} else {
		callback += "?"
	}
	callback += query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, callback, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Referer", w.client.qfhyBase+"/edu/v1/wechat/qrcodelogin")
	w.client.setCommonHeaders(req)
	resp, err := w.api.Do(req)
	if err != nil {
		return nil, fmt.Errorf("处理扫码回调: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("扫码回调返回 HTTP %d", resp.StatusCode)
	}

	sop := cookieValue(w.jar, w.client.qfhyBase, "_sop_session_")
	if sop == "" {
		return nil, errors.New("扫码成功但未获取到 _sop_session_")
	}
	claims, err := parseSopSession(sop)
	if err != nil {
		return nil, err
	}
	if claims.OpenID == "" {
		return nil, errors.New("无法从扫码会话中解析 openId")
	}
	if _, err := w.client.qfhySSO(ctx, w.api, claims.OpenID); err != nil {
		return nil, err
	}
	if cookieValue(w.jar, w.client.qfhyBase, "SESSION") == "" {
		return nil, errors.New("SSO 未返回 qfhy SESSION")
	}
	if claims.StudentID == "" || claims.Name == "" {
		user, err := w.client.qfhyCurrentUser(ctx, w.api, claims.OpenID)
		if err != nil && claims.StudentID == "" {
			return nil, err
		}
		if claims.StudentID == "" {
			claims.StudentID = user.StudentID
		}
		if claims.Name == "" {
			claims.Name = user.Name
		}
	}
	if claims.StudentID == "" {
		return nil, errors.New("扫码登录未获取到学号")
	}
	cookies := cookieString(w.jar, w.client.qfhyBase)
	if cookies == "" {
		return nil, errors.New("扫码登录未生成 Cookie")
	}
	expiresAt := claims.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(wechatSessionDays * 24 * time.Hour)
	}
	return &WechatLoginResult{
		StudentID: claims.StudentID,
		Name:      claims.Name,
		OpenID:    claims.OpenID,
		Cookies:   cookies,
		ExpiresAt: expiresAt,
	}, nil
}

func (c *Client) WechatSession(ctx context.Context, cookies string) (*Session, string, time.Time, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if err := setCookies(jar, c.qfhyBase, cookies); err != nil {
		return nil, "", time.Time{}, err
	}
	sop := cookieValue(jar, c.qfhyBase, "_sop_session_")
	if sop == "" {
		return nil, "", time.Time{}, ErrWechatSessionExpired
	}
	claims, err := parseSopSession(sop)
	if err != nil || claims.OpenID == "" {
		return nil, "", time.Time{}, ErrWechatSessionExpired
	}
	if !claims.ExpiresAt.IsZero() && claims.ExpiresAt.Before(time.Now()) {
		return nil, "", time.Time{}, ErrWechatSessionExpired
	}
	api := &http.Client{Timeout: c.timeout, Jar: jar}
	if _, err := c.qfhySSO(ctx, api, claims.OpenID); err != nil {
		if cookieValue(jar, c.qfhyBase, "SESSION") == "" {
			return nil, "", time.Time{}, fmt.Errorf("%w: %v", ErrWechatSessionExpired, err)
		}
	}
	if cookieValue(jar, c.qfhyBase, "SESSION") == "" {
		return nil, "", time.Time{}, ErrWechatSessionExpired
	}
	expiresAt := claims.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(wechatSessionDays * 24 * time.Hour)
	}
	return &Session{
		client: c,
		hc:     api,
		jar:    jar,
		openID: claims.OpenID,
	}, cookieString(jar, c.qfhyBase), expiresAt, nil
}

func (c *Client) qfhySSO(ctx context.Context, api *http.Client, openID string) (string, error) {
	target := c.qfhyBase + qfhyQRAdminPath + "?open_id=" + url.QueryEscape(openID)
	ssoURL := c.qfhyBase + qfhySSOLoginURLPath + "?service=" + url.QueryEscape(target)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ssoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Referer", target)
	c.setCommonHeaders(req)
	resp, err := api.Do(req)
	if err != nil {
		return "", fmt.Errorf("SSO 换取 SESSION: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("SSO 接口返回 HTTP %d", resp.StatusCode)
	}
	return target, nil
}

type qfhyUser struct {
	StudentID string
	Name      string
}

func (c *Client) qfhyCurrentUser(ctx context.Context, api *http.Client, openID string) (qfhyUser, error) {
	target := c.qfhyBase + qfhyQRAdminPath + "?open_id=" + url.QueryEscape(openID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.qfhyBase+qfhyCurrentUserPath, nil)
	if err != nil {
		return qfhyUser{}, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", target)
	req.Header.Set("appcode", "qddk")
	c.setCommonHeaders(req)
	resp, err := api.Do(req)
	if err != nil {
		return qfhyUser{}, fmt.Errorf("获取扫码用户信息: %w", err)
	}
	defer resp.Body.Close()
	raw, err := readBody(resp.Body, maxBody)
	if err != nil {
		return qfhyUser{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return qfhyUser{}, fmt.Errorf("用户信息接口返回 HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return qfhyUser{}, fmt.Errorf("解析扫码用户信息: %w", err)
	}
	if out.Code != http.StatusOK || out.Data.Code == "" {
		return qfhyUser{}, fmt.Errorf("获取扫码用户信息失败: %s", nonEmpty(out.Msg, "服务端返回为空"))
	}
	return qfhyUser{StudentID: out.Data.Code, Name: out.Data.Name}, nil
}

func (c *Client) setWechatAPIHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Referer", c.qfhyBase+qfhyAdminPath)
	c.setCommonHeaders(req)
}

func parseSopSession(jwt string) (sopClaims, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return sopClaims{}, errors.New("扫码会话不是有效的 JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return sopClaims{}, fmt.Errorf("解码扫码会话: %w", err)
	}
	var raw struct {
		UID   json.RawMessage `json:"uid"`
		Exp   float64         `json:"exp"`
		Extra json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return sopClaims{}, fmt.Errorf("解析扫码会话: %w", err)
	}
	claims := sopClaims{}
	if len(raw.UID) > 0 {
		if err := json.Unmarshal(raw.UID, &claims.StudentID); err != nil {
			var number json.Number
			if err := json.Unmarshal(raw.UID, &number); err == nil {
				claims.StudentID = number.String()
			}
		}
	}
	if raw.Exp > 0 {
		claims.ExpiresAt = time.Unix(int64(raw.Exp), 0)
	}
	if len(raw.Extra) > 0 {
		var extra struct {
			OpenID   string `json:"openId"`
			UserName string `json:"userName"`
		}
		var extraString string
		if err := json.Unmarshal(raw.Extra, &extraString); err == nil {
			_ = json.Unmarshal([]byte(extraString), &extra)
		} else {
			_ = json.Unmarshal(raw.Extra, &extra)
		}
		claims.OpenID = extra.OpenID
		claims.Name = extra.UserName
	}
	return claims, nil
}

func cookieValue(jar http.CookieJar, base, name string) string {
	u, err := url.Parse(base + "/")
	if err != nil {
		return ""
	}
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func cookieString(jar http.CookieJar, base string) string {
	u, err := url.Parse(base + "/")
	if err != nil {
		return ""
	}
	cookies := jar.Cookies(u)
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func setCookies(jar http.CookieJar, base, raw string) error {
	u, err := url.Parse(base + "/")
	if err != nil {
		return err
	}
	for _, part := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		jar.SetCookies(u, []*http.Cookie{{
			Name:  strings.TrimSpace(name),
			Value: value,
			Path:  "/",
		}})
	}
	return nil
}
