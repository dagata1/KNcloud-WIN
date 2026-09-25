package main

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

func ipToU32(ip net.IP) uint32 { return binary.BigEndian.Uint32(ip.To4()) }

func TestRangeToCIDRs(t *testing.T) {
	// 简单用例逐一验证
	check := func(s, e uint64, want []string) {
		t.Helper()
		got := rangeToCIDRs(s, e)
		var gs []string
		for _, c := range got {
			gs = append(gs, c.String())
		}
		if strings.Join(gs, ",") != strings.Join(want, ",") {
			t.Fatalf("rangeToCIDRs(%d,%d) = %v, want %v", s, e, gs, want)
		}
	}
	check(1<<24, (1<<24)+255, []string{"1.0.0.0/24"})
	check(0, 0, []string{"0.0.0.0/32"})
	check((4<<24)+1, (4<<24)+5, []string{"4.0.0.1/32", "4.0.0.2/31", "4.0.0.4/31"})
	check(0, (1<<32)-1, []string{"0.0.0.0/0"})
}

func TestBypassCnPolicyRoutes(t *testing.T) {
	routes, err := sstapPolicyRoutes("bypass-cn")
	if err != nil {
		t.Fatalf("sstapPolicyRoutes(bypass-cn): %v", err)
	}
	if len(routes) < 1000 {
		t.Fatalf("bypass routes too few: %d", len(routes))
	}

	// 1) 不与大陆 CIDR、保留网段重叠
	for _, r := range routes {
		if r.IP.To4() == nil {
			t.Fatalf("non-IPv4 route: %s", r.String())
		}
		start := uint64(ipToU32(r.IP))
		ones, _ := r.Mask.Size()
		end := start + (uint64(1) << (32 - ones)) - 1
		// 与每个大陆/保留区间做精确重叠检查
		for _, line := range append(strings.Split(cnRoutesTxt, "\n"), reservedCIDRs...) {
			_, ipnet, err := net.ParseCIDR(strings.TrimSpace(line))
			if err != nil {
				continue
			}
			cs := uint64(ipToU32(ipnet.IP))
			cones, _ := ipnet.Mask.Size()
			ce := cs + (uint64(1) << (32 - cones)) - 1
			if start <= ce && cs <= end {
				t.Fatalf("route %s overlaps %s", r.String(), ipnet.String())
			}
		}
	}

	// 2) 覆盖性抽查：已知海外 IP 必须落在某条路由内，国内 IP 必须不在
	mustContain := []string{"8.8.8.8", "1.1.1.1", "104.16.132.229"}
	for _, s := range mustContain {
		ip := net.ParseIP(s)
		u := uint64(ipToU32(ip))
		ok := false
		for _, r := range routes {
			ones, _ := r.Mask.Size()
			rs := uint64(ipToU32(r.IP))
			re := rs + (uint64(1) << (32 - ones)) - 1
			if u >= rs && u <= re {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("expected bypass route covering %s", s)
		}
	}
	mustNotContain := []string{"119.29.29.29", "223.5.5.5", "211.136.17.107", "114.114.114.114", "192.168.1.1", "10.0.0.1"}
	for _, s := range mustNotContain {
		ip := net.ParseIP(s)
		u := uint64(ipToU32(ip))
		for _, r := range routes {
			ones, _ := r.Mask.Size()
			rs := uint64(ipToU32(r.IP))
			re := rs + (uint64(1) << (32 - ones)) - 1
			if u >= rs && u <= re {
				t.Fatalf("route %s unexpectedly covers CN/private IP %s", r.String(), s)
			}
		}
	}
	t.Logf("bypass routes: %d", len(routes))
}

func TestBestRouteFromRows(t *testing.T) {
	// mkRoute 生成旧式 DWORD 路由行（IP 存网络序 DWORD）
	mkRoute := func(destCIDR, nextHop string, ifIdx, metric uint32) mibIPForwardRow {
		_, ipnet, err := net.ParseCIDR(destCIDR)
		if err != nil {
			t.Fatal(err)
		}
		nh := uint32(0)
		if nextHop != "" {
			nh = ipToDword(net.ParseIP(nextHop))
		}
		return mibIPForwardRow{
			Dest:    ipToDword(ipnet.IP),
			Mask:    ipToDword(net.IP(ipnet.Mask)),
			NextHop: nh,
			IfIndex: ifIdx,
			Metric1: metric,
		}
	}
	rows := []mibIPForwardRow{
		mkRoute("0.0.0.0/0", "172.16.0.1", 5, 200),   // 物理默认路由
		mkRoute("0.0.0.0/0", "10.0.0.1", 7, 9000),    // 另一默认路由（metric 更差）
		mkRoute("172.16.0.0/12", "", 5, 1),           // on-link 局域网（PPPoE 场景）
		mkRoute("104.16.0.0/32", "172.16.0.1", 5, 1), // 与目标无关的 host 路由
		mkRoute("0.0.0.0/0", "172.19.0.1", 99, 1),    // TUN 网卡上的路由，应被排除
	}

	// 海外 IP：无更长前缀命中 → 默认路由里 metric 最小的胜出
	r, ok := bestRouteFromRows(rows, net.ParseIP("8.8.8.8"), 99)
	if !ok || dwordToIP(r.NextHop).String() != "172.16.0.1" {
		t.Fatalf("expected default via 172.16.0.1, got ok=%v nextHop=%v", ok, dwordToIP(r.NextHop))
	}

	// 局域网 IP：最长前缀 /12 胜出，NextHop 保持 on-link (0.0.0.0)
	r, ok = bestRouteFromRows(rows, net.ParseIP("172.16.3.9"), 99)
	if !ok || r.NextHop != 0 {
		t.Fatalf("expected on-link /12 route, got ok=%v nextHop=%v", ok, dwordToIP(r.NextHop))
	}

	// 最优默认路由缺失时，回退到另一条默认路由
	withoutBest := []mibIPForwardRow{rows[1], rows[2], rows[3], rows[4]}
	r, ok = bestRouteFromRows(withoutBest, net.ParseIP("8.8.8.8"), 99)
	if !ok || dwordToIP(r.NextHop).String() != "10.0.0.1" {
		t.Fatalf("expected fallback default via 10.0.0.1, got ok=%v nextHop=%v", ok, dwordToIP(r.NextHop))
	}

	// 只剩 TUN 网卡上的路由 → 全部被排除，无可用路由
	if _, ok := bestRouteFromRows(rows[4:5], net.ParseIP("8.8.8.8"), 99); ok {
		t.Fatalf("expected no route")
	}
}

func TestGetIpForwardTableAndBestRoute(t *testing.T) {
	// 真机路由表读取：旧 API 必须能枚举且字段与 route print 语义一致
	rows, err := getIpForwardTable()
	if err != nil {
		t.Skipf("getIpForwardTable failed (non-Windows?): %v", err)
	}
	if len(rows) < 3 {
		t.Fatalf("route table suspiciously small: %d rows", len(rows))
	}
	defaultN := 0
	for _, r := range rows {
		if r.Mask == 0 && r.Dest == 0 {
			defaultN++
			t.Logf("default route: gw=%s ifIdx=%d metric=%d type=%d",
				dwordToIP(r.NextHop), r.IfIndex, r.Metric1, r.Type)
		}
	}
	if defaultN == 0 {
		t.Fatalf("no default route found in %d rows", len(rows))
	}

	// GetBestRoute：海外 IP 必须解析出一条可用路由
	if br, ok := getBestRoute(net.ParseIP("8.8.8.8")); ok {
		t.Logf("GetBestRoute(8.8.8.8): gw=%s ifIdx=%d metric=%d",
			dwordToIP(br.NextHop), br.IfIndex, br.Metric1)
		if br.IfIndex == 0 {
			t.Fatalf("GetBestRoute returned ifIdx 0")
		}
	} else {
		t.Fatalf("GetBestRoute failed")
	}
}

// TestParseOwnWintunDeviceIDs 覆盖残留 wintun 设备识别。
// 关键回归点：pnputil 实际输出的实例 ID 是 "SWD\Wintun\"（仅首字母大写），
// 旧实现按全大写 "SWD\WINTUN\" 做大小写敏感匹配，导致永远清理不掉残留设备。
