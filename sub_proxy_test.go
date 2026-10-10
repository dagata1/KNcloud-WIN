package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestSubscriptionFetchUsesLocalProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer origin.Close()
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()
	_, ps, _ := net.SplitHostPort(proxy.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)

	a := &App{coreRunning: true, settings: AppSettings{HttpPort: port}}
	got, err := a.fetchSubscriptionContent(origin.URL)
	if err != nil || got != "via-proxy" || hits != 1 {
		t.Fatalf("want via proxy, got %q err=%v hits=%d", got, err, hits)
	}

	a.coreRunning = false
	got, err = a.fetchSubscriptionContent(origin.URL)
	if err != nil || got != "ok" {
		t.Fatalf("core stopped: want direct, got %q err=%v", got, err)
	}

	// 代理端口不通 → 回退直连
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, ds, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	dp, _ := strconv.Atoi(ds)
	a.coreRunning, a.settings.HttpPort = true, dp
	got, err = a.fetchSubscriptionContent(origin.URL)
	if err != nil || got != "ok" {
		t.Fatalf("dead proxy: want direct fallback, got %q err=%v", got, err)
	}
}
