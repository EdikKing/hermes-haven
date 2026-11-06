package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type loginSession struct {
	Username    string
	DisplayName string
	ExpiresAt   time.Time
}

func (s loginSession) identity() map[string]any {
	return map[string]any{"ok": true, "username": s.Username, "display_name": s.DisplayName}
}

func (s *server) authRequest(ctx context.Context, method, path string, body []byte, cookies []*http.Cookie) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.dashboardURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	return s.authClient.Do(req)
}

func (s *server) authenticate(ctx context.Context, username, password string) (loginSession, int, string) {
	invalid := loginSession{}
	body, err := json.Marshal(map[string]string{"provider": s.authProvider, "username": username, "password": password, "next": "/"})
	if err != nil {
		return invalid, 400, "登录请求无效"
	}
	// 密码仅转发给配置的 Dashboard；禁止跟随重定向，也不向其发送 API 密钥。
	res, err := s.authRequest(ctx, "POST", "/auth/password-login", body, nil)
	if err != nil {
		return invalid, 502, "无法连接 Hermes 登录服务，请稍后重试"
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case 401:
		return invalid, 401, "Hermes 用户名或密码不正确"
	case 429:
		return invalid, 429, "Hermes 登录尝试次数过多，请稍后重试"
	case 404:
		return invalid, 503, "Hermes 尚未启用所配置的用户名/密码登录方式"
	case 200:
	default:
		return invalid, 502, "Hermes 登录服务暂时不可用，请稍后重试"
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&result) != nil || !result.OK || len(res.Cookies()) == 0 {
		return invalid, 502, "Hermes 登录响应无效"
	}
	// 用刚签发的 Cookie 再确认身份；上游 Cookie 只在本次验证中使用，不落盘、不返回浏览器。
	me, err := s.authRequest(ctx, "GET", "/api/auth/me", nil, res.Cookies())
	if err != nil {
		return invalid, 502, "无法确认 Hermes 登录身份，请稍后重试"
	}
	defer me.Body.Close()
	var identity struct {
		UserID      string  `json:"user_id"`
		DisplayName string  `json:"display_name"`
		Provider    string  `json:"provider"`
		ExpiresAt   float64 `json:"expires_at"`
	}
	if me.StatusCode != 200 || json.NewDecoder(io.LimitReader(me.Body, 64<<10)).Decode(&identity) != nil || identity.UserID == "" || identity.Provider != s.authProvider {
		return invalid, 502, "Hermes 未能确认登录身份，请稍后重试"
	}
	now := time.Now()
	expires := time.Unix(int64(identity.ExpiresAt), 0)
	if !expires.After(now) {
		return invalid, 401, "Hermes 登录已过期，请重新登录"
	}
	if limit := now.Add(24 * time.Hour); expires.After(limit) {
		expires = limit
	}
	return loginSession{Username: identity.UserID, DisplayName: identity.DisplayName, ExpiresAt: expires}, 200, ""
}
