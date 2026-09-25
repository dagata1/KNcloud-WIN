package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// webLoginTimeout 等待网页授权回调的最长时间，超时自动关闭本地回调服务
const webLoginTimeout = 5 * time.Minute

// 网页授权流程（与 Android 端 WEB_AUTH_INTEGRATION.md 同源，回调方式不同）：
//  1. 客户端在 127.0.0.1 随机端口起一个一次性 HTTP 回调服务
//  2. 打开系统浏览器访问 官网/#/login?from=win_auth&callback=http://127.0.0.1:{port}/auth/callback
//  3. 用户在网页完成登录并授权后，网页重定向到 callback 并带上 token（auth_data）与 email
//  4. 客户端收到回调即完成登录、拉取订阅，并通过 Wails 事件通知前端刷新界面
//
// 前端事件：
//   - "kncloud:web-login"       携带 AccountInfo，登录成功
//   - "kncloud:web-login-error" 携带错误信息（超时 / 回调失败 / 换订阅失败）

type webLoginResult struct {
	token string
	email string
}

type webLoginManager struct {
	mu   sync.Mutex
	ln   net.Listener
	srv  *http.Server
	done chan *webLoginResult
}

func (m *webLoginManager) stopLocked() {
	if m.srv != nil {
		_ = m.srv.Close()
	}
	if m.ln != nil {
		_ = m.ln.Close()
	}
	m.srv = nil
	m.ln = nil
	m.done = nil
}

// webLoginCallbackBase 是回调路径的固定前缀，其后还要拼一段随机秘密。
const webLoginCallbackBase = "/auth/callback/"

// newWebLoginSecret 生成回调路径中的随机秘密（128 bit）。
func newWebLoginSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newWebLoginHandler 构造一次性回调处理器。
//
// 这里的安全模型值得说清楚：回调服务监听在 127.0.0.1 的随机端口上，
// 而浏览器里**任何**网页都能向 127.0.0.1 发起跨源请求（img/script 这类
// 子资源请求不受同源策略阻拦，只是读不到响应 —— 但本接口的副作用是登录，
// 不需要读响应）。端口只有约 16 bit 熵，恶意页面几千个 <img> 就能喷完，
// 一旦命中就能把客户端登入攻击者的账户，进而让用户使用攻击者下发的
// 代理节点 —— 全部流量都会经过攻击者。
//
// 因此回调路径里带一段 128 bit 随机秘密，只有拿到授权 URL 的官网知道。
// 另外拒绝明显属于子资源的请求（Sec-Fetch-Dest: image/script/...），
// 真正的回调是一次顶层导航，永远不会是这些值。
func newWebLoginHandler(secret string, resCh chan<- *webLoginResult) http.Handler {
	want := []byte(secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// 子资源请求不可能是授权回调（真实回调是顶层导航 document）
		switch r.Header.Get("Sec-Fetch-Dest") {
		case "image", "script", "style", "font", "audio", "video", "object", "embed":
			w.WriteHeader(http.StatusForbidden)
			return
		}
		got := []byte(strings.TrimPrefix(r.URL.Path, webLoginCallbackBase))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		q := r.URL.Query()
		token := q.Get("token")
		if token == "" {
			token = q.Get("auth_data")
		}
		if token == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(webLoginHTML("授权失败：回调中缺少 token 参数，请重试")))
			return
		}
		select {
		case resCh <- &webLoginResult{token: token, email: q.Get("email")}:
		default: // 已经收到过一次，忽略重复回调
		}
		_, _ = w.Write([]byte(webLoginHTML("登录成功！请回到 KNcloud-WIN 客户端继续使用，本页面可以关闭。")))
	})
}

// StartWebLogin 打开浏览器进行网页登录授权，返回本次打开的授权 URL。
// 结果通过事件 kncloud:web-login / kncloud:web-login-error 异步通知前端。
func (a *App) StartWebLogin() (string, error) {
	a.mu.RLock()
	domain := a.account.Domain
	a.mu.RUnlock()
	if domain == "" {
		domain = kncloudDefaultDomain
	}

	// 惰性初始化回调服务管理器（指针化，防止拷贝内部互斥锁）；并发启动时由 a.mu 串行化
	a.mu.Lock()
	if a.webLogin == nil {
		a.webLogin = &webLoginManager{}
	}
	m := a.webLogin
	a.mu.Unlock()
	m.mu.Lock()
	m.stopLocked() // 重复点击时先关掉上一次的回调服务

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("failed to start local callback server: %v", err)
	}
	secret, err := newWebLoginSecret()
	if err != nil {
		_ = ln.Close()
		m.mu.Unlock()
		return "", fmt.Errorf("failed to generate callback secret: %v", err)
	}
	resCh := make(chan *webLoginResult, 1)
	port := ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.Handle(webLoginCallbackBase, newWebLoginHandler(secret, resCh))
	srv := &http.Server{
		Handler: mux,
		// 本地进程也可能挂着连接不发数据，给个读头超时避免占住服务
		ReadHeaderTimeout: 10 * time.Second,
	}
	m.ln = ln
	m.srv = srv
	m.done = resCh
	m.mu.Unlock()

	go func() { _ = srv.Serve(ln) }()

	callbackURL := fmt.Sprintf("http://127.0.0.1:%d%s%s", port, webLoginCallbackBase, secret)
	loginURL := strings.TrimRight(domain, "/") + "/#/login?from=win_auth&callback=" + url.QueryEscape(callbackURL)

	if err := openInDefaultBrowser(loginURL); err != nil {
		a.stopWebLogin()
		return "", fmt.Errorf("failed to open browser: %v", err)
	}
	a.addLogInternal("info", fmt.Sprintf("Web login started, waiting for callback on 127.0.0.1:%d", port))

	go func() {
		var res *webLoginResult
		select {
		case res = <-resCh:
		case <-time.After(webLoginTimeout):
			res = nil
		}
		a.stopWebLogin()

		ctx := a.appCtx()
		if res == nil {
			a.addLogInternal("warn", "Web login timed out waiting for browser callback")
			if ctx != nil {
				runtime.EventsEmit(ctx, "kncloud:web-login-error", "等待网页授权超时，请重试")
			}
			return
		}
		// 邮箱优先级：回调参数 > getSubscribe 响应（completeLogin 内处理）
		email := strings.TrimSpace(res.email)
		acct, err := a.completeLogin(domain, email, res.token)
		if err != nil {
			a.addLogInternal("error", fmt.Sprintf("Web login failed: %v", err))
			if ctx != nil {
				runtime.EventsEmit(ctx, "kncloud:web-login-error", err.Error())
			}
			return
		}
		a.addLogInternal("info", fmt.Sprintf("Web login succeeded for %s", acct.Email))
		if ctx != nil {
			runtime.EventsEmit(ctx, "kncloud:web-login", acct)
		}
	}()

	return loginURL, nil
}

// CancelWebLogin 主动取消网页登录等待，关闭本地回调服务
func (a *App) CancelWebLogin() {
	a.stopWebLogin()
}

func (a *App) stopWebLogin() {
	a.mu.Lock()
	m := a.webLogin
	a.mu.Unlock()
	stopWebLoginManager(m)
}

// stopWebLoginLocked 与 stopWebLogin 等价，但要求调用方已持有 a.mu。
//
// 为什么需要这个变体：a.mu 是 sync.RWMutex，不可重入。cleanup() 在持写锁的状态下
// 清理各子系统，如果那里直接调 stopWebLogin()，它会再次 a.mu.Lock() 而永久阻塞 ——
// 表现为「点退出后进程卡死」，且卡在还原系统代理之后、停内核/停 TUN/存盘之前，
// 导致 TUN 路由残留 + 本次会话配置丢失。见 TestCleanupDoesNotDeadlock。
func (a *App) stopWebLoginLocked() {
	stopWebLoginManager(a.webLogin)
}

// stopWebLoginManager 关闭回调服务，是上面两个入口的公共实现。
// m 为 nil（用户从未走过网页登录）时直接返回。
func stopWebLoginManager(m *webLoginManager) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

func openInDefaultBrowser(rawURL string) error {
	// domain 来自配置文件，可能被改成 file:// 之类的协议；FileProtocolHandler
	// 会照单全收地交给系统处理器执行。这里限定只放行 http/https。
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("refusing to open non-http(s) URL: %s", u.Scheme)
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
}

// webLoginHTML 回调落地页：告知用户授权结果并引导返回客户端
func webLoginHTML(msg string) string {
	return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>KNcloud-WIN 授权</title></head>` +
		`<body style="display:flex;align-items:center;justify-content:center;height:100vh;margin:0;` +
		`font-family:system-ui,-apple-system,'Segoe UI','Microsoft YaHei',sans-serif;background:#181818;color:#fff;">` +
		`<div style="text-align:center;"><div style="font-size:20px;font-weight:600;margin-bottom:12px;">KNcloud-WIN</div>` +
		`<div style="font-size:14px;opacity:.8;">` + html.EscapeString(msg) + `</div></div></body></html>`
}
