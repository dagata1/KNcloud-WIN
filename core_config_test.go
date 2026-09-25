package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestVisionNodeSkipsMux XTLS Vision（flow 非空）节点即使全局开启 mux 也不得启用 mux
// （服务端会把携带 TCP 请求的 mux 连接断开 —— P0 修复回归测试）。
func TestVisionNodeSkipsMux(t *testing.T) {
	node := NodeItem{
		Protocol: "VLESS", Name: "vision", Address: "example.com", Port: 443,
		UUID: "u", Security: "tls", Network: "tcp",
		Flow: "xray-rprx-vision",
	}
	out, err := buildProxyOutbound(node, true)
	if err != nil {
		t.Fatalf("buildProxyOutbound: %v", err)
	}
	if _, has := out["mux"]; has {
		t.Fatalf("Vision node must not enable mux")
	}
	// buildProxyOutbound 用 Go 字面量构造，vnext/users 均为 []map[string]interface{}（非 JSON 反序列化的 []interface{}）
	user := out["settings"].(map[string]interface{})["vnext"].([]map[string]interface{})[0]["users"].([]map[string]interface{})[0]
	if user["flow"] != "xray-rprx-vision" {
		t.Fatalf("flow not preserved: %v", user)
	}

	// 无 flow 的 VLESS 节点：mux 正常启用
	node.Flow = ""
	out, err = buildProxyOutbound(node, true)
	if err != nil {
		t.Fatalf("buildProxyOutbound: %v", err)
	}
	mux, ok := out["mux"].(map[string]interface{})
	if !ok || mux["enabled"] != true {
		t.Fatalf("mux expected for plain VLESS node, got %v", out["mux"])
	}

	// VMess / Trojan 同样受 flow gate（flow 只在 VLESS 上出现，这里验证不影响）
	node.Protocol = "VMess"
	out, _ = buildProxyOutbound(node, true)
	if _, has := out["mux"]; !has {
		t.Fatalf("mux expected for VMess node")
	}
}

// buildTestApp 构造带设置的 App（仅用于配置生成测试）。
func buildTestApp(mode string, mux bool) *App {
	return &App{
		settings: AppSettings{
			SocksPort: 10808, HttpPort: 10809, MuxEnabled: mux,
			DnsServers: "1.1.1.1", CoreType: "Xray-core",
		},
		routingMode: mode,
	}
}

func extractRules(t *testing.T, cfgJSON string) []map[string]interface{} {
	t.Helper()
	var cfg struct {
		Routing struct {
			Rules []map[string]interface{} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		t.Fatalf("config json: %v", err)
	}
	return cfg.Routing.Rules
}

// TestProxyCnModeRoutesCnViaProxy proxy-cn 模式：CN 走 proxy、其余默认 direct
// （系统代理模式下实现「仅代理国内」，修复旧版等同 global 的行为）。
func TestProxyCnModeRoutesCnViaProxy(t *testing.T) {
	cfg, err := buildTestApp("proxy-cn", false).buildCoreConfigJSON(NodeItem{Protocol: "Shadowsocks", Address: "a", Port: 1, Method: "aes-128-gcm", UUID: "p"})
	if err != nil {
		t.Fatalf("buildCoreConfigJSON: %v", err)
	}
	rules := extractRules(t, cfg)
	last := rules[len(rules)-1]
	if last["outboundTag"] != "direct" {
		t.Fatalf("proxy-cn: default rule should be direct, got %v", last)
	}
	found := false
	for _, r := range rules {
		ips, _ := r["ip"].([]interface{})
		if len(ips) > 0 && r["outboundTag"] == "proxy" {
			for _, ip := range ips {
				if s, _ := ip.(string); s == "geoip:cn" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("proxy-cn: no CN->proxy rule in %v", rules)
	}
}

// TestBypassCnModeRoutesCnDirect bypass-cn 模式保持：CN 直连、其余默认 proxy。
func TestBypassCnModeRoutesCnDirect(t *testing.T) {
	cfg, err := buildTestApp("bypass-cn", false).buildCoreConfigJSON(NodeItem{Protocol: "Shadowsocks", Address: "a", Port: 1, Method: "aes-128-gcm", UUID: "p"})
	if err != nil {
		t.Fatalf("buildCoreConfigJSON: %v", err)
	}
	rules := extractRules(t, cfg)
	last := rules[len(rules)-1]
	if last["outboundTag"] != "proxy" {
		t.Fatalf("bypass-cn: default rule should be proxy, got %v", last)
	}
	cnDirect := false
	for _, r := range rules {
		ips, _ := r["ip"].([]interface{})
		for _, ip := range ips {
			if s, _ := ip.(string); s == "geoip:cn" && r["outboundTag"] == "direct" {
				cnDirect = true
			}
		}
	}
	if !cnDirect {
		t.Fatalf("bypass-cn: expected CN->direct rule in %v", rules)
	}
}

// TestPinnedAssetHashesMatch 内置 geo 数据与 wintun.dll 的固定哈希必须与实际文件一致
// （更新这些文件时需同步更新 pin，否则运行时会反复重写落地文件）。
func TestPinnedAssetHashesMatch(t *testing.T) {
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		src, err := geoAssets.Open("geo/" + name)
		if err != nil {
			t.Fatalf("open embedded %s: %v", name, err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, src); err != nil {
			src.Close()
			t.Fatalf("hash %s: %v", name, err)
		}
		src.Close()
		if got := hex.EncodeToString(h.Sum(nil)); got != pinnedGeoSHA256[name] {
			t.Fatalf("%s hash drift:\n got %s\nwant %s", name, got, pinnedGeoSHA256[name])
		}
	}
	h := sha256.Sum256(wintunDLL)
	if got := hex.EncodeToString(h[:]); got != pinnedWintunSHA256 {
		t.Fatalf("wintun.dll hash drift:\n got %s\nwant %s", got, pinnedWintunSHA256)
	}
}

// TestFileSHA256Matches fileSHA256Matches 的三种分支。
func TestFileSHA256Matches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(p, []byte("hello kncloud"), 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello kncloud"))
	want := hex.EncodeToString(sum[:])
	if !fileSHA256Matches(p, want) {
		t.Fatal("expected match for identical content")
	}
	if fileSHA256Matches(p, "deadbeef") {
		t.Fatal("expected mismatch for wrong hash")
	}
	if fileSHA256Matches(filepath.Join(dir, "missing.bin"), want) {
		t.Fatal("expected mismatch for missing file")
	}
	if fileSHA256Matches(p, "") {
		t.Fatal("empty pin must never match")
	}
}

// TestAllowInsecureFlowFromShareLink 分享链接 allowInsecure=1 一路传导到内核 TLS 配置。
func TestAllowInsecureFlowFromShareLink(t *testing.T) {
	link := "vless://u1@example.com:443?security=tls&sni=example.com&allowInsecure=1&type=tcp"
	node, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !node.AllowInsecure {
		t.Fatal("allowInsecure=1 not parsed")
	}
	out, err := buildProxyOutbound(node, false)
	if err != nil {
		t.Fatalf("buildProxyOutbound: %v", err)
	}
	tls := out["streamSettings"].(map[string]interface{})["tlsSettings"].(map[string]interface{})
	if tls["allowInsecure"] != true {
		t.Fatalf("allowInsecure not applied to tlsSettings: %v", tls)
	}

	// 默认（不带参数）：保持证书校验
	node2, _ := ParseShareLink("vless://u1@example.com:443?security=tls&sni=example.com&type=tcp")
	out2, _ := buildProxyOutbound(node2, false)
	tls2 := out2["streamSettings"].(map[string]interface{})["tlsSettings"].(map[string]interface{})
	if tls2["allowInsecure"] != false {
		t.Fatalf("allowInsecure must default to false, got %v", tls2["allowInsecure"])
	}
}

// TestConfigJSONEncodable 生成的内核配置必须是合法 JSON 且包含必要段。
func TestConfigJSONEncodable(t *testing.T) {
	cfg, err := buildTestApp("global", false).buildCoreConfigJSON(NodeItem{Protocol: "Trojan", Address: "a", Port: 443, UUID: "pw", Security: "tls", Network: "ws", Path: "/ws"})
	if err != nil {
		t.Fatalf("buildCoreConfigJSON: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		t.Fatalf("config not valid json: %v", err)
	}
	for _, k := range []string{"inbounds", "outbounds", "routing", "dns"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("config missing %q section", k)
		}
	}
	if !bytes.Contains([]byte(cfg), []byte(`"protocol":"trojan"`)) {
		t.Fatal("trojan outbound missing")
	}
}

// TestBuildProxyOutboundProtocolShapes 各协议 outbound 形状回归：
// VLESS/VMess/Trojan/Shadowsocks × tls/reality/none × ws/grpc/httpupgrade，
// 以及 allowInsecure 透传、SNI 回退、SS 旧字段拆分、不支持协议报错。
func TestBuildProxyOutboundProtocolShapes(t *testing.T) {
	settings := func(out map[string]interface{}) map[string]interface{} {
		t.Helper()
		return out["settings"].(map[string]interface{})
	}
	stream := func(out map[string]interface{}) map[string]interface{} {
		t.Helper()
		return out["streamSettings"].(map[string]interface{})
	}

	// --- VLESS + tls + ws：SNI 缺省回退 Address，allowInsecure/FP 透传，Host 头 ---
	out, err := buildProxyOutbound(NodeItem{
		Protocol: "VLESS", Name: "v", Address: "a.example", Port: 443,
		UUID: "uid", Security: "tls", Network: "ws", AllowInsecure: true, FP: "safari",
	}, false)
	if err != nil {
		t.Fatalf("VLESS: %v", err)
	}
	if out["protocol"] != "vless" || out["tag"] != "proxy" {
		t.Fatalf("VLESS protocol/tag: %v", out)
	}
	tlsS := stream(out)["tlsSettings"].(map[string]interface{})
	if tlsS["serverName"] != "a.example" {
		t.Fatalf("serverName should fall back to Address: %v", tlsS)
	}
	if tlsS["allowInsecure"] != true || tlsS["fingerprint"] != "safari" {
		t.Fatalf("allowInsecure/fingerprint not passed: %v", tlsS)
	}
	ws := stream(out)["wsSettings"].(map[string]interface{})
	if ws["path"] != "/" {
		t.Fatalf("ws path default: %v", ws)
	}

	// --- VLESS + reality：默认指纹 chrome ---
	out, _ = buildProxyOutbound(NodeItem{
		Protocol: "VLESS", Address: "r.example", Port: 443, UUID: "u2",
		Security: "reality", Network: "tcp", SNI: "s.example", PBK: "pk", SID: "si",
	}, false)
	reality := stream(out)["realitySettings"].(map[string]interface{})
	if reality["fingerprint"] != "chrome" || reality["publicKey"] != "pk" || reality["shortId"] != "si" || reality["serverName"] != "s.example" {
		t.Fatalf("reality settings: %v", reality)
	}
	if stream(out)["security"] != "reality" {
		t.Fatalf("reality security: %v", stream(out))
	}

	// --- VMess：alterId/security ---
	out, _ = buildProxyOutbound(NodeItem{
		Protocol: "VMess", Address: "m.example", Port: 1, UUID: "u3", AlterID: 8, Security: "none", Network: "tcp",
	}, true)
	users := settings(out)["vnext"].([]map[string]interface{})[0]["users"].([]map[string]interface{})
	if users[0]["alterId"] != 8 || users[0]["security"] != "auto" {
		t.Fatalf("vmess user: %v", users[0])
	}
	if _, has := out["mux"]; !has {
		t.Fatalf("vmess mux expected when enabled")
	}

	// --- Trojan：servers/password，无 flow 时 mux ---
	out, _ = buildProxyOutbound(NodeItem{
		Protocol: "Trojan", Address: "t.example", Port: 443, UUID: "pass", Security: "tls", Network: "grpc", ServiceName: "svc",
	}, true)
	servers := settings(out)["servers"].([]map[string]interface{})
	if servers[0]["password"] != "pass" {
		t.Fatalf("trojan password: %v", servers[0])
	}
	if stream(out)["grpcSettings"].(map[string]interface{})["serviceName"] != "svc" {
		t.Fatalf("grpc serviceName: %v", stream(out))
	}

	// --- Shadowsocks：method 字段优先；旧格式 "method:password" 拆分 ---
	out, _ = buildProxyOutbound(NodeItem{
		Protocol: "Shadowsocks", Address: "s.example", Port: 8388, Method: "aes-256-gcm", UUID: "pw",
	}, false)
	ss := settings(out)["servers"].([]map[string]interface{})[0]
	if ss["method"] != "aes-256-gcm" || ss["password"] != "pw" {
		t.Fatalf("shadowsocks server: %v", ss)
	}
	out, _ = buildProxyOutbound(NodeItem{
		Protocol: "Shadowsocks", Address: "s.example", Port: 8388, UUID: "chacha20-ietf-poly1305:secret",
	}, false)
	ss = settings(out)["servers"].([]map[string]interface{})[0]
	if ss["method"] != "chacha20-ietf-poly1305" || ss["password"] != "secret" {
		t.Fatalf("shadowsocks legacy split: %v", ss)
	}
	if _, err := buildProxyOutbound(NodeItem{Protocol: "Shadowsocks", Address: "x", Port: 1, UUID: "no-colon"}, false); err == nil {
		t.Fatalf("shadowsocks without method should error")
	}

	// --- httpupgrade ---
	out, _ = buildProxyOutbound(NodeItem{
		Protocol: "VLESS", Address: "h.example", Port: 443, UUID: "u", Security: "tls",
		Network: "httpupgrade", Path: "/path", HostName: "cdn.example",
	}, false)
	hu := stream(out)["httpupgradeSettings"].(map[string]interface{})
	if hu["path"] != "/path" || hu["host"] != "cdn.example" {
		t.Fatalf("httpupgrade settings: %v", hu)
	}

	// --- 不支持的协议 ---
	if _, err := buildProxyOutbound(NodeItem{Protocol: "Hysteria2", Address: "x", Port: 1}, false); err == nil {
		t.Fatalf("Hysteria2 should be rejected")
	}

	// --- 无 flow 的 VLESS 开 mux：concurrency 固定 8 ---
	out, _ = buildProxyOutbound(NodeItem{Protocol: "VLESS", Address: "a", Port: 1, UUID: "u", Security: "tls", Network: "tcp"}, true)
	mux := out["mux"].(map[string]interface{})
	if mux["enabled"] != true || mux["concurrency"] != 8 {
		t.Fatalf("mux shape: %v", mux)
	}
}
