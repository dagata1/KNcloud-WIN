package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newCallbackServer(t *testing.T) (*httptest.Server, string, chan *webLoginResult) {
	t.Helper()
	secret, err := newWebLoginSecret()
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan *webLoginResult, 1)
	mux := http.NewServeMux()
	mux.Handle(webLoginCallbackBase, newWebLoginHandler(secret, ch))
	return httptest.NewServer(mux), secret, ch
}

func callbackGet(t *testing.T, url string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestLegitCallbackSucceeds 正常回调（顶层导航）应当成功并交出 token。
func TestLegitCallbackSucceeds(t *testing.T) {
	srv, secret, ch := newCallbackServer(t)
	defer srv.Close()

	code := callbackGet(t, fmt.Sprintf("%s%s%s?token=REAL&email=u@e.com", srv.URL, webLoginCallbackBase, secret),
		map[string]string{"Sec-Fetch-Dest": "document"})
	if code != http.StatusOK {
		t.Fatalf("正常回调应 200，实际 %d", code)
	}
	select {
	case r := <-ch:
		if r.token != "REAL" || r.email != "u@e.com" {
			t.Fatalf("回调数据有误: %+v", r)
		}
	default:
		t.Fatal("未收到回调结果")
	}
}

// TestDriveByPortSprayBlocked 核心攻击场景：恶意网页知道端口、但猜不到路径秘密。
// 这正是加固前可以得手的攻击 —— 几千个 <img> 喷端口即可把客户端登入攻击者账户。
func TestDriveByPortSprayBlocked(t *testing.T) {
	srv, _, ch := newCallbackServer(t)
	defer srv.Close()

	// 加固前的固定路径
	if code := callbackGet(t, srv.URL+"/auth/callback?token=ATTACKER", nil); code == http.StatusOK {
		t.Fatal("旧的固定路径不应再被接受")
	}
	// 猜测各种路径
	for _, p := range []string{
		"/auth/callback/", "/auth/callback/0", "/auth/callback/deadbeef",
		"/auth/callback/00000000000000000000000000000000",
	} {
		if code := callbackGet(t, srv.URL+p+"?token=ATTACKER", nil); code == http.StatusOK {
			t.Fatalf("路径 %s 不应被接受", p)
		}
	}
	select {
	case r := <-ch:
		t.Fatalf("攻击者 token 被接受了: %+v", r)
	default:
	}
}

// TestSubresourceRequestsRejected 即使路径秘密泄漏，子资源请求（img/script）
// 也不可能是真实回调，必须拒绝。
func TestSubresourceRequestsRejected(t *testing.T) {
	srv, secret, ch := newCallbackServer(t)
	defer srv.Close()

	for _, dest := range []string{"image", "script", "style", "font", "audio", "video", "object", "embed"} {
		url := fmt.Sprintf("%s%s%s?token=ATTACKER", srv.URL, webLoginCallbackBase, secret)
		if code := callbackGet(t, url, map[string]string{"Sec-Fetch-Dest": dest}); code != http.StatusForbidden {
			t.Fatalf("Sec-Fetch-Dest=%s 应被拒绝(403)，实际 %d", dest, code)
		}
	}
	select {
	case r := <-ch:
		t.Fatalf("子资源请求不应产生登录: %+v", r)
	default:
	}
}

// TestNonGetRejected 只接受 GET。
func TestNonGetRejected(t *testing.T) {
	srv, secret, _ := newCallbackServer(t)
	defer srv.Close()
	url := fmt.Sprintf("%s%s%s?token=X", srv.URL, webLoginCallbackBase, secret)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req, _ := http.NewRequest(m, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s 应 405，实际 %d", m, resp.StatusCode)
		}
	}
}

// TestMissingTokenRejected 路径对但没带 token。
func TestMissingTokenRejected(t *testing.T) {
	srv, secret, ch := newCallbackServer(t)
	defer srv.Close()
	code := callbackGet(t, fmt.Sprintf("%s%s%s", srv.URL, webLoginCallbackBase, secret), nil)
	if code != http.StatusBadRequest {
		t.Fatalf("缺 token 应 400，实际 %d", code)
	}
	select {
	case <-ch:
		t.Fatal("不应产生结果")
	default:
	}
}

// TestSecretIsUnguessable 秘密必须是 128 bit 十六进制且每次不同。
func TestSecretIsUnguessable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := newWebLoginSecret()
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != 32 {
			t.Fatalf("秘密长度应为 32 个十六进制字符，实际 %d", len(s))
		}
		if seen[s] {
			t.Fatal("出现重复秘密")
		}
		seen[s] = true
	}
}

// TestWebLoginHTMLEscapes 提示文案必须转义，避免将来传入外部数据时变成 HTML 注入。
func TestWebLoginHTMLEscapes(t *testing.T) {
	out := webLoginHTML(`<script>alert(1)</script>`)
	if strings.Contains(out, "<script>") {
		t.Fatalf("未转义: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("应输出转义后的文本: %s", out)
	}
}
