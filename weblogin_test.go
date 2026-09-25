package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// callbackURLFrom 从 StartWebLogin 返回的授权 URL 中取出本地回调地址。
// 回调路径带一段随机秘密（见 newWebLoginHandler），测试不能再自行拼固定路径。
func callbackURLFrom(t *testing.T, loginURL string) string {
	t.Helper()
	u, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("授权 URL 无法解析: %v", err)
	}
	// 站点地址形如 https://host/#/login?from=win_auth&callback=...
	q := u.Query()
	cb := q.Get("callback")
	if cb == "" {
		if i := strings.Index(loginURL, "callback="); i >= 0 {
			cb, _ = url.QueryUnescape(loginURL[i+len("callback="):])
		}
	}
	if cb == "" {
		t.Fatalf("授权 URL 中找不到 callback 参数: %s", loginURL)
	}
	return cb
}

// TestWebLoginCallback 验证网页授权回传链路：StartWebLogin 起本地回调服务 →
// 模拟网页授权后重定向（GET /auth/callback?token=..&email=..）→
// completeLogin 被触发（token 无效时记录失败日志，不落地登录态）。
func TestWebLoginCallback(t *testing.T) {
	// StartWebLogin 会通过 rundll32 拉起系统默认浏览器 —— CI 上既没有浏览器
	// 也不该弹窗，故 -short 模式跳过。本机开发时正常执行。
	if testing.Short() {
		t.Skip("skipping in -short mode: opens the system default browser")
	}
	app := NewApp()

	loginURL, err := app.StartWebLogin()
	if err != nil {
		t.Fatalf("StartWebLogin failed: %v", err)
	}
	defer app.stopWebLogin()
	cb := callbackURLFrom(t, loginURL)

	// 模拟网页授权后的重定向
	resp, err := http.Get(cb + "?token=fake-token&email=test@example.com")
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("callback HTTP %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "KNcloud-WIN") {
		t.Fatalf("unexpected callback page: %s", body)
	}

	// fake token 无法换取订阅：completeLogin 应失败并留下错误日志
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range app.GetLogs() {
			if l.Level == "error" && strings.Contains(l.Message, "Web login failed") {
				return // 链路走通
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("expected 'Web login failed' log entry after invalid-token callback")
}

// TestWebLoginMissingToken 缺少 token 参数时回调应返回 400
func TestWebLoginMissingToken(t *testing.T) {
	// 同 TestWebLoginCallback：会拉起系统浏览器，-short 模式跳过
	if testing.Short() {
		t.Skip("skipping in -short mode: opens the system default browser")
	}
	app := NewApp()
	loginURL, err := app.StartWebLogin()
	if err != nil {
		t.Fatalf("StartWebLogin failed: %v", err)
	}
	defer app.stopWebLogin()
	cb := callbackURLFrom(t, loginURL)

	resp, err := http.Get(cb + "?email=test@example.com")
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected HTTP 400 for missing token, got %d", resp.StatusCode)
	}
}
