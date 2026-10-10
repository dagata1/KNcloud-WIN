package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"v2rayN-win11/internal/subfetch"
)

// 订阅一律经本地 HTTP 代理（真实内核，直连配置）；内核没在运行时等一会儿后报错，不直连。
func TestSubscriptionFetchAlwaysViaLocalProxy(t *testing.T) {
	var hits int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("sub-content"))
	}))
	defer origin.Close()

	old := subProxyWait
	subProxyWait = 500 * time.Millisecond
	t.Cleanup(func() { subProxyWait = old })

	a := newTestApp(t)
	// 内核没在运行：报错，源站一次都没被直连
	if _, err := a.fetchSubscriptionContent(origin.URL); !errors.Is(err, subfetch.ErrProxyUnavailable) {
		t.Fatalf("core not running: want ErrProxyUnavailable, got %v", err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("origin contacted directly while the core was down")
	}

	// 内核（无节点 → 直连配置）起来后经本地代理拉取
	a.mu.Lock()
	if err := a.startCoreLocked(); err != nil {
		a.mu.Unlock()
		t.Fatal(err)
	}
	a.coreRunning = true
	a.mu.Unlock()
	got, err := a.fetchSubscriptionContent(origin.URL)
	if err != nil || got != "sub-content" || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("via local proxy: got %q err=%v hits=%d", got, err, hits)
	}
}
