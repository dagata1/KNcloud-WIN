package main

import (
	"os"
	"path/filepath"
	"testing"
)

// setConfigDir 把用户配置目录指到临时目录（跨平台：Windows 读 %AppData%，其余读 XDG_CONFIG_HOME）。
func setConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}

func writeConfig(t *testing.T, content string) {
	t.Helper()
	d, err := appConfigDir()
	if err != nil {
		t.Fatalf("appConfigDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d, "config.json"), []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestLoadPersistedRoundTrip 完整配置 → 加载：字段、激活节点恢复、显式关闭的设置不被默认值覆盖。
func TestLoadPersistedRoundTrip(t *testing.T) {
	setConfigDir(t)
	writeConfig(t, `{
		"nodes": [
			{"id": "n1", "name": "A", "protocol": "VLESS", "address": "a.example", "port": 443},
			{"id": "n2", "name": "B", "protocol": "Trojan", "address": "b.example", "port": 443}
		],
		"subscriptions": [{"id": "s1", "name": "sub", "url": "https://x/sub", "nodeCount": 2}],
		"settings": {"socksPort": 10808, "httpPort": 10809, "muxEnabled": true,
			"minimizeToTray": false, "autoStart": false, "autoConnect": false,
			"coreType": "Xray-core", "dnsServers": "1.1.1.1", "uiMode": "simple", "theme": "dark"},
		"routingMode": "global",
		"activeNodeID": "n2",
		"totalUp": 1234,
		"totalDown": 5678,
		"lastSystemProxy": true
	}`)
	a := &App{}
	if !a.loadPersisted() {
		t.Fatalf("loadPersisted should succeed with valid config")
	}
	// 激活节点按 activeNodeID 恢复（数据文件里的 active 标记不可信）
	if len(a.nodes) != 2 {
		t.Fatalf("nodes: %v", a.nodes)
	}
	if !a.nodes[1].Active || a.nodes[0].Active {
		t.Fatalf("active node should be n2: %+v", a.nodes)
	}
	if a.activeNodeID != "n2" {
		t.Fatalf("activeNodeID: %q", a.activeNodeID)
	}
	// 显式 false 的设置必须保持 false（不能被旧版默认值覆盖）
	if a.settings.MinimizeToTray || a.settings.AutoStart || a.settings.AutoConnect {
		t.Fatalf("explicit false settings overridden: %+v", a.settings)
	}
	if !a.settings.MuxEnabled || a.settings.SocksPort != 10808 || a.settings.HttpPort != 10809 {
		t.Fatalf("settings not loaded: %+v", a.settings)
	}
	if a.settings.UiMode != "simple" || a.settings.Theme != "dark" {
		t.Fatalf("ui settings: %+v", a.settings)
	}
	if a.routingMode != "global" {
		t.Fatalf("routingMode: %q", a.routingMode)
	}
	if a.totalUpBytes != 1234 || a.totalDownBytes != 5678 {
		t.Fatalf("totals: %d/%d", a.totalUpBytes, a.totalDownBytes)
	}
	if !a.lastSystemProxy {
		t.Fatalf("lastSystemProxy should be true")
	}
	if len(a.subscriptions) != 1 || a.subscriptions[0].NodeCount != 2 {
		t.Fatalf("subscriptions: %v", a.subscriptions)
	}
}

// TestLoadPersistedLegacyDefaults 旧版配置（缺新字段）→ 默认开启三项 + 系统代理恢复。
func TestLoadPersistedLegacyDefaults(t *testing.T) {
	setConfigDir(t)
	writeConfig(t, `{
		"nodes": [],
		"settings": {"socksPort": 10808, "httpPort": 10809}
	}`)
	a := &App{}
	if !a.loadPersisted() {
		t.Fatalf("loadPersisted should succeed")
	}
	if !a.settings.MinimizeToTray {
		t.Fatalf("minimizeToTray default should be true for legacy config")
	}
	if !a.settings.AutoStart {
		t.Fatalf("autoStart default should be true for legacy config")
	}
	if !a.settings.AutoConnect {
		t.Fatalf("autoConnect default should be true for legacy config")
	}
	if !a.lastSystemProxy {
		t.Fatalf("lastSystemProxy default should be true for legacy config (旧行为启动即开系统代理)")
	}
}

// TestLoadPersistedFirstRun 无配置文件 → false（首次运行），不写入任何文件。
func TestLoadPersistedFirstRun(t *testing.T) {
	setConfigDir(t)
	a := &App{}
	if a.loadPersisted() {
		t.Fatalf("first run should return false")
	}
	if len(a.nodes) != 0 || a.settings.SocksPort != 0 {
		t.Fatalf("first run should leave zero values")
	}
}

// TestLoadPersistedCorruptJSON 损坏的 JSON → false，不 panic。
func TestLoadPersistedCorruptJSON(t *testing.T) {
	setConfigDir(t)
	writeConfig(t, `{"nodes": [broken`)
	a := &App{}
	if a.loadPersisted() {
		t.Fatalf("corrupt config should return false")
	}
}

// TestLoadPersistedRejectsNoPort settings.socksPort=0 视为无效配置（返回 false 走首次运行初始化）。
func TestLoadPersistedRejectsNoPort(t *testing.T) {
	setConfigDir(t)
	writeConfig(t, `{"settings": {"socksPort": 0}}`)
	a := &App{}
	if a.loadPersisted() {
		t.Fatalf("config without socksPort should be rejected")
	}
}

// TestLoadPersistedActiveFallback activeNodeID 指向不存在的节点时回退到第一个节点。
func TestLoadPersistedActiveFallback(t *testing.T) {
	setConfigDir(t)
	writeConfig(t, `{
		"nodes": [{"id": "n1", "name": "A", "protocol": "VLESS", "address": "a", "port": 1}],
		"settings": {"socksPort": 10808},
		"activeNodeID": "ghost"
	}`)
	a := &App{}
	if !a.loadPersisted() {
		t.Fatalf("loadPersisted should succeed")
	}
	if !a.nodes[0].Active || a.activeNodeID != "n1" {
		t.Fatalf("should fall back to first node: %+v active=%v", a.nodes[0], a.activeNodeID)
	}
}

// TestPersistedSaveLoadRoundTrip save → load 往返一致（不含 Account：DPAPI 依赖 Windows 用户态）。
func TestPersistedSaveLoadRoundTrip(t *testing.T) {
	setConfigDir(t)
	src := &App{
		nodes: []NodeItem{
			{ID: "x1", Name: "节点", Protocol: "VMess", Address: "v.example", Port: 1, Active: true},
		},
		subscriptions: []SubscriptionItem{{ID: "s", Name: "n", URL: "https://u"}},
		settings: AppSettings{
			SocksPort: 10808, HttpPort: 10809, MuxEnabled: true, CoreType: "Xray-core",
			DnsServers: "8.8.8.8", MinimizeToTray: true,
		},
		routingMode:     "bypass-cn",
		activeNodeID:    "x1",
		totalUpBytes:    9,
		totalDownBytes:  99,
		lastSystemProxy: true,
	}
	src.savePersisted()

	dst := &App{}
	if !dst.loadPersisted() {
		t.Fatalf("reload should succeed")
	}
	if dst.routingMode != src.routingMode || dst.activeNodeID != src.activeNodeID {
		t.Fatalf("routing/active mismatch: %q %q", dst.routingMode, dst.activeNodeID)
	}
	if len(dst.nodes) != 1 || dst.nodes[0].ID != "x1" || !dst.nodes[0].Active {
		t.Fatalf("nodes roundtrip: %+v", dst.nodes)
	}
	if dst.settings != src.settings {
		t.Fatalf("settings roundtrip:\n got %+v\nwant %+v", dst.settings, src.settings)
	}
	if dst.totalUpBytes != 9 || dst.totalDownBytes != 99 || !dst.lastSystemProxy {
		t.Fatalf("totals/proxy roundtrip: %d %d %v", dst.totalUpBytes, dst.totalDownBytes, dst.lastSystemProxy)
	}
}

// TestSettingsHasKey / TestTopLevelHasKey 字段存在性判断（区分「缺字段」与「显式 false」）。
func TestSettingsHasKey(t *testing.T) {
	data := []byte(`{"settings": {"autoStart": false, "name": ""}, "lastSystemProxy": false}`)
	if !settingsHasKey(data, "autoStart") {
		t.Fatalf("explicit false key should exist")
	}
	if settingsHasKey(data, "minimizeToTray") {
		t.Fatalf("missing key should not exist")
	}
	if settingsHasKey(data, "lastSystemProxy") {
		// lastSystemProxy 只存在于顶层，settings 作用域里查不到
		t.Fatalf("top-level-only key should not exist in settings scope")
	}
	if settingsHasKey([]byte(`not json`), "autoStart") {
		t.Fatalf("corrupt json should return false")
	}
	if settingsHasKey([]byte(`{"no-settings": {}}`), "autoStart") {
		t.Fatalf("missing settings object should return false")
	}
}

func TestTopLevelHasKey(t *testing.T) {
	if !topLevelHasKey([]byte(`{"lastSystemProxy": true}`), "lastSystemProxy") {
		t.Fatalf("existing key should be found")
	}
	if topLevelHasKey([]byte(`{}`), "lastSystemProxy") {
		t.Fatalf("missing key should not be found")
	}
	if topLevelHasKey([]byte(`bad`), "x") {
		t.Fatalf("corrupt json should return false")
	}
}
