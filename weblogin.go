package main

import (
	_ "embed"
	"encoding/json"
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
	// id 标识当前生效的 session。重复发起登录会换掉 done 通道，
	// 旧 goroutine 超时后若仍调用 stopWebLogin() 会把新 session 一起关掉，
	// 导致新登录误报超时。带上 id 后只关闭自己那一轮。
	id int64
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
	resCh := make(chan *webLoginResult, 1)
	port := ln.Addr().(*net.TCPAddr).Port
	sessionID := time.Now().UnixNano()
	st := &webLoginStatus{state: "waiting"}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(webLoginLogo)
	})
	mux.HandleFunc("/auth/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(st.get())
	})
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		token := q.Get("token")
		if token == "" {
			token = q.Get("auth_data")
		}
		email := q.Get("email")
		if token == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(webLoginHTML("error", "授权失败", "回调中缺少登录凭证，请回到客户端重新发起登录。")))
			return
		}
		select {
		case resCh <- &webLoginResult{token: token, email: email}:
			st.set("syncing", "")
		default:
		}
		// 先显示「正在同步订阅」，页面轮询 /auth/status 拿到客户端真正的结果再变成成功 / 失败，
		// 不再在拉订阅之前就宣称登录成功。
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(webLoginHTML("syncing", "正在同步订阅", "已授权，客户端正在拉取订阅信息…")))
	})
	srv := &http.Server{Handler: mux}
	m.ln = ln
	m.srv = srv
	m.done = resCh
	m.id = sessionID
	m.mu.Unlock()

	go func() { _ = srv.Serve(ln) }()

	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port)
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
		// 只关闭自己这一轮 session：重复发起登录时旧 goroutine 若仍调用
		// stopWebLogin() 会把新 session 一起关掉，导致新登录误报超时。
		// 收到回调后先不关：浏览器页面还要轮询 /auth/status 显示最终结果，结果出来后再留一会儿。
		finish := func() {
			time.AfterFunc(webLoginLinger, func() { a.stopWebLoginSession(sessionID) })
		}

		ctx := a.appCtx()
		if res == nil {
			a.stopWebLoginSession(sessionID)
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
			st.set("error", err.Error())
			finish()
			a.addLogInternal("error", fmt.Sprintf("Web login failed: %v", err))
			if ctx != nil {
				runtime.EventsEmit(ctx, "kncloud:web-login-error", err.Error())
			}
			return
		}
		st.set("ok", acct.Email)
		finish()
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
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

// stopWebLoginSession 只关闭指定 id 的 session。
// 重复发起登录时，旧 goroutine 超时后不应把新 session 一起关掉。
func (a *App) stopWebLoginSession(id int64) {
	a.mu.Lock()
	m := a.webLogin
	a.mu.Unlock()
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.id != id {
		return // 已被更新的 session 取代，不动
	}
	m.stopLocked()
}

func openInDefaultBrowser(rawURL string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
}

// webLoginLinger 出结果后回调服务再保留多久（给浏览器页面轮询到最终状态）。
const webLoginLinger = 60 * time.Second

//go:embed build/appicon.png
var webLoginLogo []byte

// webLoginStatus 本轮授权的进度，供落地页轮询：waiting → syncing → ok / error。
type webLoginStatus struct {
	mu    sync.Mutex
	state string
	msg   string
}

func (s *webLoginStatus) set(state, msg string) {
	s.mu.Lock()
	s.state, s.msg = state, msg
	s.mu.Unlock()
}

func (s *webLoginStatus) get() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]string{"state": s.state, "message": s.msg}
}

// webLoginHTML 回调落地页：KNcloud 图标 + 状态卡片，跟随系统深浅色。
// state 为 syncing 时页面轮询 /auth/status，按客户端真实结果切换为成功 / 失败。
// 文案必须转义：token/email 来自 URL 参数，将来若拼进文案会形成 XSS；
// 轮询结果用 textContent 写入，同样不会被当成 HTML。
func webLoginHTML(state, title, msg string) string {
	return strings.NewReplacer(
		"{{STATE}}", html.EscapeString(state),
		"{{TITLE}}", html.EscapeString(title),
		"{{MSG}}", html.EscapeString(msg),
	).Replace(webLoginPage)
}

const webLoginPage = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>KNcloud 授权</title>
<link rel="icon" href="/auth/logo.png">
<style>
:root{--bg:#fafafa;--card:#fff;--fg:#09090b;--mut:#71717a;--bd:#e4e4e7;--ok:#16a34a;--err:#dc2626;--spin:#a1a1aa}
@media (prefers-color-scheme:dark){:root{--bg:#09090b;--card:#18181b;--fg:#fafafa;--mut:#a1a1aa;--bd:#27272a;--ok:#22c55e;--err:#f87171;--spin:#52525b}}
*{box-sizing:border-box}
html,body{height:100%;margin:0}
body{display:flex;align-items:center;justify-content:center;background:var(--bg);color:var(--fg);
font-family:system-ui,-apple-system,"Segoe UI","Microsoft YaHei UI","Microsoft YaHei",sans-serif;-webkit-font-smoothing:antialiased;padding:24px}
.card{width:100%;max-width:380px;background:var(--card);border:1px solid var(--bd);border-radius:16px;padding:36px 32px 28px;text-align:center;
box-shadow:0 1px 2px rgba(0,0,0,.04),0 8px 24px rgba(0,0,0,.06)}
.logo{width:64px;height:64px;border-radius:16px;display:block;margin:0 auto 14px}
.brand{font-size:13px;font-weight:600;letter-spacing:.02em;color:var(--mut);margin-bottom:26px}
.icon{width:44px;height:44px;margin:0 auto 16px;display:flex;align-items:center;justify-content:center}
.spin{width:30px;height:30px;border:3px solid var(--bd);border-top-color:var(--fg);border-radius:50%;animation:r .8s linear infinite}
@keyframes r{to{transform:rotate(360deg)}}
.badge{width:44px;height:44px;border-radius:50%;display:flex;align-items:center;justify-content:center}
.ok .badge{background:color-mix(in srgb,var(--ok) 14%,transparent);color:var(--ok)}
.error .badge{background:color-mix(in srgb,var(--err) 14%,transparent);color:var(--err)}
h1{font-size:18px;font-weight:600;margin:0 0 8px}
p{font-size:14px;line-height:1.6;color:var(--mut);margin:0;word-break:break-word}
.foot{margin-top:26px;padding-top:16px;border-top:1px solid var(--bd);font-size:12px;color:var(--mut)}
</style></head>
<body><div class="card {{STATE}}" id="card">
<img class="logo" src="/auth/logo.png" alt="KNcloud">
<div class="brand">KNcloud for Windows</div>
<div class="icon" id="icon"></div>
<h1 id="title">{{TITLE}}</h1>
<p id="msg">{{MSG}}</p>
<div class="foot" id="foot">请保持客户端运行</div>
</div>
<script>
(function(){
var card=document.getElementById('card'),icon=document.getElementById('icon'),
title=document.getElementById('title'),msg=document.getElementById('msg'),foot=document.getElementById('foot');
var OK='<div class="badge"><svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg></div>';
var ERR='<div class="badge"><svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18M6 6l12 12"/></svg></div>';
function show(s,t,m){
  card.className='card '+s;
  icon.innerHTML=s==='ok'?OK:s==='error'?ERR:'<div class="spin"></div>';
  if(t)title.textContent=t;
  if(m!=null)msg.textContent=m;
  foot.textContent=s==='ok'?'现在可以关闭此页面，回到客户端继续使用':s==='error'?'请回到客户端重新登录':'请保持客户端运行';
}
show('{{STATE}}');
if('{{STATE}}'!=='syncing')return;
var tries=0;
function poll(){
  fetch('/auth/status',{cache:'no-store'}).then(function(r){return r.json()}).then(function(d){
    if(d.state==='ok'){show('ok','登录成功',d.message?('已登录 '+d.message+'，订阅已同步。'):'订阅已同步。');return}
    if(d.state==='error'){show('error','同步订阅失败',d.message||'');return}
    if(++tries<240)setTimeout(poll,800);
  }).catch(function(){
    if(++tries<240)setTimeout(poll,1500);else show('error','无法获取结果','请回到客户端查看登录状态。');
  });
}
setTimeout(poll,600);
})();
</script></body></html>`
