package main

import (
	"net"
	"testing"
)

// TestTunDNSUpstream TUN 模式下的 DNS 上游选择。
// 这条路径只能用纯 IPv4：relayDNS 以原始 UDP 经绑定物理网卡的 IPv4 socket 发出，
// DoH/DoT 用不了，域名会成环，IPv6 发不出去。
func TestTunDNSUpstream(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"", defaultTunDNS, "空配置回退默认"},
		{"   ", defaultTunDNS, "全空白回退默认"},
		{"1.1.1.1", "1.1.1.1:53", "纯 IP 自动补 53 端口"},
		{"1.1.1.1, 8.8.8.8, 223.5.5.5", "1.1.1.1:53", "取第一个可用"},
		{"8.8.8.8:5353", "8.8.8.8:5353", "保留自定义端口"},
		{" 9.9.9.9 ", "9.9.9.9:53", "去除空白"},
		{"https://dns.google/dns-query", defaultTunDNS, "DoH 在原始 UDP 通道用不了"},
		{"tls://1.1.1.1", defaultTunDNS, "DoT 同理"},
		{"dns.google", defaultTunDNS, "域名会成环，跳过"},
		{"2001:4860:4860::8888", defaultTunDNS, "IPv6 无法从 IPv4 socket 发出"},
		{"[2001:4860:4860::8888]:53", defaultTunDNS, "带端口的 IPv6 同样跳过"},
		{"https://dns.google/dns-query, 8.8.4.4", "8.8.4.4:53", "跳过不可用项后取到可用项"},
		{"localhost", defaultTunDNS, "非 IP 主机名跳过"},
		{"not-an-ip, , 114.114.114.114", "114.114.114.114:53", "容错空项与垃圾项"},
	}
	for _, c := range cases {
		if got := tunDNSUpstream(c.in); got != c.want {
			t.Errorf("%s: tunDNSUpstream(%q) = %q, want %q", c.why, c.in, got, c.want)
		}
	}
}

// TestTunDNSUpstreamAlwaysUsable 无论输入多离谱，都必须返回可直接 Dial 的 host:port，
// 否则 DNS 通道会在运行期才炸。
func TestTunDNSUpstreamAlwaysUsable(t *testing.T) {
	for _, in := range []string{"", "garbage", "https://x/y", "::1", "1.1.1.1", "a,b,c", ",,,"} {
		got := tunDNSUpstream(in)
		h, p, err := net.SplitHostPort(got)
		if err != nil {
			t.Fatalf("输入 %q 产生了无法解析的地址 %q: %v", in, got, err)
		}
		if h == "" || p == "" {
			t.Fatalf("输入 %q 产生了不完整的地址 %q", in, got)
		}
		if net.ParseIP(h) == nil {
			t.Fatalf("输入 %q 产生了非 IP 上游 %q", in, got)
		}
	}
}
