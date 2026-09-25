package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// startTestCallbackServer 起 weblogin 回调服务（不经 StartWebLogin，避免真的拉起浏览器）。
func startTestCallbackServer(t *testing.T) (*App, *webLoginManager, int, string) {
	t.Helper()
	app := NewApp()
	app.mu.Lock()
	app.webLogin = &webLoginManager{}
	m := app.webLogin
	app.mu.Unlock()

	m.mu.Lock()
	port, err := m.startCallbackServer()
	if err != nil {
		m.mu.Unlock()
		t.Fatalf("startCallbackServer failed: %v", err)
	}
	cbPath := m.cbPath
	m.mu.Unlock()

	t.Cleanup(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.stopLocked()
	})
	return app, m, port, cbPath
}

// TestWebLoginCallbackRoundTrip 正确路径 + token → 200，结果送达内部通道。
func TestWebLoginCallbackRoundTrip(t *testing.T) {
	_, m, port, cbPath := startTestCallbackServer(t)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s?token=fake-token&email=test@example.com", port, cbPath))
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("callback HTTP %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "KNcloud-WIN") {
		t.Fatalf("unexpected callback page: %s", body)
	}

	select {
	case res := <-m.done:
		if res.token != "fake-token" || res.email != "test@example.com" {
			t.Fatalf("wrong result delivered: %+v", res)
		}
	default:
		t.Fatal("callback result not delivered to channel")
	}
}

// TestWebLoginCallbackRejectsUnknownPath 未知/旧版固定路径一律 404（防登录 CSRF 的核心断言）。
func TestWebLoginCallbackRejectsUnknownPath(t *testing.T) {
	_, _, port, _ := startTestCallbackServer(t)

	for _, p := range []string{"/auth/callback", "/auth/callback/wrong-secret", "/"} {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s?token=attacker-token", port, p))
		if err != nil {
			t.Fatalf("request %s failed: %v", p, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("path %s: expected 404, got %d", p, resp.StatusCode)
		}
	}
}

// TestWebLoginCallbackMissingToken 带正确路径但缺 token → 400。
func TestWebLoginCallbackMissingToken(t *testing.T) {
	_, _, port, cbPath := startTestCallbackServer(t)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s?email=test@example.com", port, cbPath))
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected HTTP 400 for missing token, got %d", resp.StatusCode)
	}
}

// TestWebLoginCallbackSecretRandomized 每次启动回调服务都应生成不同的随机路径。
func TestWebLoginCallbackSecretRandomized(t *testing.T) {
	_, m, _, first := startTestCallbackServer(t)

	m.mu.Lock()
	if _, err := m.startCallbackServer(); err != nil {
		m.mu.Unlock()
		t.Fatalf("restart callback server: %v", err)
	}
	second := m.cbPath
	m.mu.Unlock()

	if first == "" || second == "" || first == second {
		t.Fatalf("callback path not randomized: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "/auth/callback/") || len(first) < len("/auth/callback/") {
		t.Fatalf("callback path %q lacks secret segment", first)
	}
}
