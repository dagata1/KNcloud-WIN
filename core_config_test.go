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
	user := out["settings"].(map[string]interface{})["vnext"].([]interface{})[0].(map[string]interface{})["users"].([]interface{})[0].(map[string]interface{})
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
