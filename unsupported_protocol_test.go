package main

import (
	"strings"
	"testing"
)

// TestUnsupportedReasonCoversXrayProtocols 校验判据与 Xray 实际能力一致。
func TestUnsupportedReasonCoversXrayProtocols(t *testing.T) {
	for _, p := range []string{"VLESS", "VMess", "Trojan", "Shadowsocks"} {
		if r := unsupportedReason(p); r != "" {
			t.Fatalf("%s 应当被支持，却返回了不支持原因：%s", p, r)
		}
	}
	for _, p := range []string{"Hysteria2", "TUIC", "WireGuard", ""} {
		if unsupportedReason(p) == "" {
			t.Fatalf("%q 不应被视为受支持协议", p)
		}
	}
}

// TestHysteria2ReasonIsActionable Hysteria2 是当前唯一能导入却连不上的协议，
// 提示必须说明是「协议不被支持」而非含糊的失败，否则用户会误以为是节点故障。
func TestHysteria2ReasonIsActionable(t *testing.T) {
	r := unsupportedReason("Hysteria2")
	if r == "" {
		t.Fatal("Hysteria2 必须给出不支持原因")
	}
	if !strings.Contains(r, "Hysteria2") {
		t.Fatalf("原因里应点名协议，实际：%s", r)
	}
	if !strings.Contains(r, "Xray") {
		t.Fatalf("原因里应说明是内置内核的限制，实际：%s", r)
	}
}

// TestBuildProxyOutboundRejectsHysteria2 后端底线：即便前端拦截被绕过，
// 配置生成层也必须拒绝，不能产出一份 Xray 无法理解的配置。
func TestBuildProxyOutboundRejectsHysteria2(t *testing.T) {
	node := NodeItem{
		Protocol: "Hysteria2", Name: "HY2", Address: "hy.example.net",
		Port: 24433, UUID: "pw",
	}
	if _, err := buildProxyOutbound(node, false); err == nil {
		t.Fatal("buildProxyOutbound 必须拒绝 Hysteria2")
	} else if !strings.Contains(err.Error(), "Hysteria2") {
		t.Fatalf("错误信息应点名协议，实际：%v", err)
	}
}

// TestMarkUnsupportedTagsOnlyUnsupported 打标只应作用于不受支持的节点。
func TestMarkUnsupportedTagsOnlyUnsupported(t *testing.T) {
	nodes := []NodeItem{
		{ID: "1", Protocol: "VLESS"},
		{ID: "2", Protocol: "Hysteria2"},
		{ID: "3", Protocol: "Trojan"},
	}
	markUnsupported(nodes)

	if nodes[0].Unsupported != "" || nodes[2].Unsupported != "" {
		t.Fatalf("受支持的节点不应被标记：%q / %q", nodes[0].Unsupported, nodes[2].Unsupported)
	}
	if nodes[1].Unsupported == "" {
		t.Fatal("Hysteria2 节点应被标记")
	}
	if n := countUnsupported(nodes); n != 1 {
		t.Fatalf("countUnsupported 应为 1，实际 %d", n)
	}
}

// TestUnsupportedNotPersisted Unsupported 是按当前内核能力算出来的展示字段，
// 不得写进配置文件 —— 否则将来支持了新协议，老配置里的节点会一直显示为不可用。
func TestUnsupportedNotPersisted(t *testing.T) {
	n := NodeItem{ID: "1", Protocol: "VLESS", Name: "n"}
	if n.Unsupported != "" {
		t.Fatal("新建节点不应带 Unsupported")
	}
	// 该字段带 omitempty：为空时不出现在 JSON 里
	marked := []NodeItem{{ID: "2", Protocol: "Hysteria2"}}
	markUnsupported(marked)
	if marked[0].Unsupported == "" {
		t.Fatal("打标后应有值")
	}
}
