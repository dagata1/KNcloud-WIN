package main

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"math/bits"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wintunDLL 是 sing-box TUN 入站创建虚拟网卡所必需的驱动 DLL（amd64，匹配内嵌的
// sing-box.exe）。来自官方发行包 wintun-0.14.1.zip，未做任何修改；
// 许可见 cores/wintun-LICENSE.txt —— 允许随「仅通过其 API 使用它」的软件一同分发。
//
//go:embed cores/wintun.dll
var wintunDLL []byte

//go:embed geo/cn-routes.txt
var cnRoutesTxt string

const (
	tunLogTag      = "TUN"
	createNoWindow = 0x08000000

	// SSTap 方案核心参数（复刻 SSTap-beta 的 TAP 分流机制）
	tunIfaceName = "KNcloud-TAP"       // 固定虚拟网卡名（对应 SSTAP 1）
	tunGateway   = "172.19.0.1"        // 虚拟网卡网关地址（/30）
	tunDnsAddr   = "198.18.0.2"        // 写入虚拟网卡的系统 DNS，端口 53 被 sing-box 劫持
	tunMetric    = 1                   // 虚拟网卡接口 metric（对应 SSTap 抢占 DNS 优先级）
	routeMetric  = 5                   // 分流路由 metric
	tunGateway6  = "fdfe:dcba:9876::1" // 虚拟网卡 IPv6 地址（/126），配合 2000::/3 分流路由堵 IPv6 泄漏
)

var (
	iphlpapi                        = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetBestRoute                = iphlpapi.NewProc("GetBestRoute")
	procGetIpForwardTable           = iphlpapi.NewProc("GetIpForwardTable")
	procCreateIpForwardEntry        = iphlpapi.NewProc("CreateIpForwardEntry")
	procDeleteIpForwardEntry        = iphlpapi.NewProc("DeleteIpForwardEntry")
	procConvertInterfaceLuidToIndex = iphlpapi.NewProc("ConvertInterfaceLuidToIndex")
)

// 保留网段：永不写入虚拟网卡（私有/链路本地/组播等，保持系统直连行为）
var reservedCIDRs = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
	"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
}

// isElevated 当前进程是否以管理员权限运行（TUN 模式必需）
func isElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	var elevation uint32
	var retLen uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevation)), uint32(unsafe.Sizeof(elevation)), &retLen); err != nil {
		return false
	}
	return elevation != 0
}

// ------------------------- 路由引擎（复刻 SSTap 的静态分流路由） -------------------------
//
// 使用旧版 IP Helper API（GetBestRoute / GetIpForwardTable / CreateIpForwardEntry /
// DeleteIpForwardEntry）：MIB_IPFORWARDROW 是纯 14-DWORD 结构，无对齐与布局歧义
// （x/sys 的 MIB_IPFORWARD_ROW2 布局与系统实测不符——实测每字段偏移 +4，读写皆错）。
// IP 地址以网络序存储于 DWORD：字段值 == binary.BigEndian.Uint32(ip)。

// mibIPForwardRow 对应 C 的 MIB_IPFORWARDROW（route.exe 同款）
type mibIPForwardRow struct {
	Dest, Mask, Policy, NextHop, IfIndex        uint32
	Type, Proto, Age, NextHopAS                 uint32
	Metric1, Metric2, Metric3, Metric4, Metric5 uint32
}

const (
	ipRouteTypeDirect   = 3 // on-link
	ipRouteTypeIndirect = 4 // 经网关
	ipProtoNetMgmt      = 3 // MIB_IPPROTO_NETMGMT：由网络管理实体（我们）写入
)

// ipToDword 将 IPv4 转为 MIB_IPFORWARDROW 字段的 DWORD。
// API 要求内存中为网络序字节（172.16.0.1 → AC 10 00 01），
// 小端机器上该 DWORD 的 Go 数值等于按 LittleEndian 读网络字节序切片。
func ipToDword(ip net.IP) uint32 {
	if v4 := ip.To4(); v4 != nil {
		return binary.LittleEndian.Uint32(v4)
	}
	return 0
}

// dwordToIP 将 MIB_IPFORWARDROW 字段 DWORD 还原为 IPv4（ipToDword 的逆变换）
func dwordToIP(v uint32) net.IP {
	b := make(net.IP, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

// getIpForwardTable 枚举系统 IPv4 路由表（调用方缓冲区版，无封送歧义）
func getIpForwardTable() ([]mibIPForwardRow, error) {
	size := uint32(0)
	ret, _, _ := procGetIpForwardTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if ret != 0 && ret != 122 { // 122 = ERROR_INSUFFICIENT_BUFFER（首次探测的正常返回）
		return nil, syscall.Errno(ret)
	}
	buf := make([]byte, size)
	ret, _, _ = procGetIpForwardTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if ret != 0 {
		return nil, syscall.Errno(ret)
	}
	if size < 4 {
		return nil, nil
	}
	rowSize := uint32(unsafe.Sizeof(mibIPForwardRow{}))
	n := binary.LittleEndian.Uint32(buf[0:4])
	if max := (size - 4) / rowSize; n > max {
		n = max
	}
	rows := make([]mibIPForwardRow, n)
	for i := uint32(0); i < n; i++ {
		copy((*[56]byte)(unsafe.Pointer(&rows[i]))[:], buf[4+i*rowSize:4+(i+1)*rowSize])
	}
	return rows, nil
}

// getBestRoute 查询系统去往 dst 的真实最优路由（GetBestRoute，排除逻辑由调用方处理）
func getBestRoute(dst net.IP) (mibIPForwardRow, bool) {
	var row mibIPForwardRow
	ret, _, _ := procGetBestRoute.Call(uintptr(ipToDword(dst)), 0, uintptr(unsafe.Pointer(&row)))
	if ret != 0 {
		return row, false
	}
	return row, true
}

// addRouteRow 通过 CreateIpForwardEntry 写入一条路由
func addRouteRow(row *mibIPForwardRow) error {
	row.Policy = 0
	row.Proto = ipProtoNetMgmt
	row.Age = 0
	row.NextHopAS = 0
	row.Metric2, row.Metric3, row.Metric4, row.Metric5 = 0, 0, 0, 0
	ret, _, _ := procCreateIpForwardEntry.Call(uintptr(unsafe.Pointer(row)))
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

// interfaceMetric4 读取网卡的 IPv4 interface metric（即 netsh 里的自动跃点）。
//
// 关键坑：Vista 之后旧版路由 API 的 MIB_IPFORWARDROW.Metric1 语义变为
// 「接口 metric + 路由 metric」的合成值（route.exe 的 "metric 1" 落库后
// 实际是 ifaceMetric+1），CreateIpForwardEntry 传入小于接口 metric 的值
// 会被 ERROR_INVALID_PARAMETER 拒绝。所以所有写路由的地方都必须把
// 目标 metric 叠加在本接口的 interface metric 之上。
func interfaceMetric4(ifIdx uint32) uint32 {
	row := windows.MibIpInterfaceRow{Family: windows.AF_INET, InterfaceIndex: ifIdx}
	if err := windows.GetIpInterfaceEntry(&row); err != nil {
		return 0
	}
	return row.Metric
}

// addRoute2 便捷封装：目的 CIDR 经指定网卡/网关写入路由。
// metric 参数为「路由 metric」，叠加在网卡 interface metric 之上（见 interfaceMetric4）。
func addRoute2(ifIdx uint32, dst net.IPNet, nextHop net.IP, metric uint32) error {
	row := mibIPForwardRow{
		Dest:    ipToDword(dst.IP),
		Mask:    ipToDword(net.IP(dst.Mask)),
		NextHop: ipToDword(nextHop),
		IfIndex: ifIdx,
		Metric1: interfaceMetric4(ifIdx) + metric,
	}
	if row.NextHop == 0 {
		row.Type = ipRouteTypeDirect
	} else {
		row.Type = ipRouteTypeIndirect
	}
	return addRouteRow(&row)
}

// deleteRouteRow 删除一条路由（按 Dest/Mask/Policy/NextHop/IfIndex 匹配）
func deleteRouteRow(row *mibIPForwardRow) error {
	ret, _, _ := procDeleteIpForwardEntry.Call(uintptr(unsafe.Pointer(row)))
	if ret != 0 {
		return syscall.Errno(ret)
	}
	return nil
}

// deleteRoutesOnInterface 删除该网卡上的全部 IPv4 路由（虚拟网卡销毁时系统也会自动回收，此处是主动清理）
func deleteRoutesOnInterface(ifIdx uint32) int {
	rows, err := getIpForwardTable()
	if err != nil {
		return 0
	}
	n := 0
	for _, r := range rows {
		if r.IfIndex != ifIdx {
			continue
		}
		row := r
		if deleteRouteRow(&row) == nil {
			n++
		}
	}
	return n
}

// bestRouteFromRows 在路由表行中查找去往 dst 的最优 IPv4 路由：
// 最长前缀优先，前缀相同取 Metric1 最小；跳过回环与被排除的接口（通常是 TUN 自己）。
// NextHop 为 0.0.0.0 的 on-link 路由（PPPoE 等场景）同样有效。
func bestRouteFromRows(rows []mibIPForwardRow, dst net.IP, excludeIfIdx uint32) (mibIPForwardRow, bool) {
	d := ipToDword(dst)
	if d == 0 && dst.To4() == nil {
		return mibIPForwardRow{}, false
	}
	best := mibIPForwardRow{}
	found := false
	for _, r := range rows {
		if r.IfIndex == excludeIfIdx || r.IfIndex == 1 {
			continue
		}
		if d&r.Mask != r.Dest&r.Mask {
			continue
		}
		ones := bits.OnesCount32(r.Mask)
		bestOnes := bits.OnesCount32(best.Mask)
		if !found || ones > bestOnes || (ones == bestOnes && r.Metric1 < best.Metric1) {
			best = r
			found = true
		}
	}
	return best, found
}

// bestRouteForIPv4 在系统当前路由表中查找去往 dst 的最优路由，
// 用于给节点服务器 IP 写 /32 直连路由防回环
// （对应 SSTap config 里的 local_connection_shortest_r_nexthop 机制）
func bestRouteForIPv4(dst net.IP, excludeIfIdx uint32) (mibIPForwardRow, bool) {
	rows, err := getIpForwardTable()
	if err != nil {
		return mibIPForwardRow{}, false
	}
	return bestRouteFromRows(rows, dst, excludeIfIdx)
}

// lookupNodeIPv4s 解析节点服务器的 IPv4 地址（最多 4 个）；Address 为 IP 字面量时直接返回
// isBogusUnicastV4 判断该 IPv4 是否不可能作为节点服务器地址（组播/保留/链路本地等）。
// 系统 DNS 对被墙域名的污染应答经常落在这类地址段，TUN 启动前必须剔除。
func isBogusUnicastV4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return true
	}
	if v4.IsUnspecified() || v4.IsLoopback() || v4.IsMulticast() ||
		v4.IsLinkLocalUnicast() || v4.IsLinkLocalMulticast() {
		return true
	}
	// 240.0.0.0/4 保留段（含 255.255.255.255，IsMulticast 不覆盖）
	if v4[0] >= 240 {
		return true
	}
	// 198.18.0.0/15 基准测试段（各类 fake-ip 方案的惯用段）
	if v4[0] == 198 && v4[1] == 18 {
		return true
	}
	return false
}

func filterValidIPv4s(ips []net.IP) []net.IP {
	var out []net.IP
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil || isBogusUnicastV4(v4) {
			continue
		}
		out = append(out, v4)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

// lookupIPv4Via 绕过系统 DNS，直接向指定公共 DNS 查询 A 记录并过滤非法地址。
// 用于系统 DNS 被污染（应答全部落在非法段）时的兜底。
func lookupIPv4Via(host, dns string) []net.IP {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", net.JoinHostPort(dns, "53"))
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	raw := make([]net.IP, 0, len(ips))
	for _, a := range ips {
		raw = append(raw, a.IP)
	}
	return filterValidIPv4s(raw)
}

// lookupNodeIPv4s 解析节点服务器的 IPv4。系统 DNS 对被墙域名可能返回污染应答
// （组播/保留段等非法地址），先解析再剔除；全部非法时改用公共 DNS
// （223.5.5.5 / 119.29.29.29）直查兜底。
func lookupNodeIPv4s(host string) []net.IP {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil && !isBogusUnicastV4(v4) {
			return []net.IP{v4}
		}
		return nil
	}
	for _, dns := range []string{"223.5.5.5", "119.29.29.29"} {
		if ips := lookupIPv4Via(host, dns); len(ips) > 0 {
			return ips
		}
	}
	return filterValidIPv4s(mustLookupIPs(host))
}

func mustLookupIPs(host string) []net.IP {
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	return ips
}

// rangeToCIDRs 将闭区间 [start, end] 拆成 CIDR 列表
func rangeToCIDRs(start, end uint64) []net.IPNet {
	var out []net.IPNet
	for start <= end {
		align := uint64(1) << 32
		if start != 0 {
			align = start & (^start + 1) // 最低置位比特 = 最大对齐块
		}
		rem := end - start + 1
		size := align
		if rem < size {
			size = 1
			for size*2 <= rem {
				size *= 2
			}
		}
		prefixLen := 32 - (bits.Len64(size) - 1)
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, uint32(start))
		mask := net.CIDRMask(prefixLen, 32)
		out = append(out, net.IPNet{IP: ip.Mask(mask), Mask: mask})
		start += size
	}
	return out
}

// computeBypassRoutes 计算需要送进虚拟网卡的 CIDR 列表 =
// 全网空间 - 中国大陆 IP（geo/cn-routes.txt）- 保留网段
// 等价 SSTap「Skip all China IP」规则集生成的路由表
// computeBypassRoutes 绕过大陆策略的分流路由（非中国大陆 CIDR 全集）。
// 计算逻辑已迁移到 sstap.go 的策略引擎（sstapPolicyRoutes）。
func computeBypassRoutes() []net.IPNet {
	routes, _ := sstapPolicyRoutes("bypass-cn")
	return routes
}

// tunVerbose 自检（--tun-selftest）时输出的逐步日志；正常运行保持安静
var tunVerbose bool

func vlog(format string, args ...interface{}) {
	if tunVerbose {
		fmt.Printf("[tun] "+format+"\n", args...)
	}
}

// applySstapRouting 虚拟网卡就绪后写入整套 SSTap 式网络配置。
// policy 决定分流路由集合（bypass-cn / global / proxy-cn / sstap:<rules 文件>）。
// 返回写入物理网卡的节点直连路由（断开时需回收）与 TUN 上生效的分流路由条数。
func applySstapRouting(node NodeItem, tunIdx uint32, policy string) (hostRoutes []mibIPForwardRow, routed int, err error) {
	hidden := &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}

	// 0) DNS 劫持生效前，先用系统 DNS 解析节点服务器地址（此刻分流路由未铺，走物理出口无回环）。
	//    系统 DNS 对被墙域名可能返回污染应答（组播/保留段），已剔除并内置公共 DNS 兜底。
	nodeIPs := lookupNodeIPv4s(node.Address)
	if len(nodeIPs) == 0 {
		return nil, 0, fmt.Errorf("failed to resolve a valid IPv4 address for node %s (system DNS may be polluted; IPv6-only nodes are not supported in TUN mode)", node.Address)
	}

	// 1) 网卡 metric 抢到最高 + 系统 DNS 指向劫持地址（等价 SSTap 设置 TAP 适配器 DNS/跃点数）
	ps := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf("Set-NetIPInterface -InterfaceIndex %d -InterfaceMetric %d; Set-DnsClientServerAddress -InterfaceIndex %d -ServerAddresses '%s'",
			tunIdx, tunMetric, tunIdx, tunDnsAddr))
	ps.SysProcAttr = hidden
	if out, err := ps.CombinedOutput(); err != nil {
		return nil, 0, fmt.Errorf("failed to configure adapter DNS/metric: %v: %s", err, strings.TrimSpace(string(out)))
	}

	gw := net.ParseIP(tunGateway)

	// 2) DNS 劫持地址送进 TUN（系统 DNS 查询会被 sing-box port53 规则接管）
	_, dnsNet, _ := net.ParseCIDR(tunDnsAddr + "/32")
	if err := addRoute2(tunIdx, *dnsNet, gw, 1); err != nil {
		return nil, 0, fmt.Errorf("failed to write DNS hijack route: %w", err)
	}

	// 3) 节点服务器 IP 写 /32 直连路由防回环（沿系统真实最优路由，支持 on-link 网关/PPPoE）
	var lastAddErr error
	for _, ip := range nodeIPs {
		base, ok := getBestRoute(ip)
		if !ok {
			vlog("no best route found for node IP %s", ip)
			continue
		}
		vlog("node %s -> base route ifIdx=%d nextHop=%s metric=%d",
			ip, base.IfIndex, dwordToIP(base.NextHop), base.Metric1)
		row := mibIPForwardRow{
			Dest:    ipToDword(ip),
			Mask:    0xFFFFFFFF,
			NextHop: base.NextHop,
			IfIndex: base.IfIndex,
			Type:    base.Type,
			// 合成 metric = 物理 NIC 接口 metric + 1（该网卡上最高优先级）
			Metric1: interfaceMetric4(base.IfIndex) + 1,
		}
		if row.Type != ipRouteTypeDirect && row.Type != ipRouteTypeIndirect {
			if row.NextHop == 0 {
				row.Type = ipRouteTypeDirect
			} else {
				row.Type = ipRouteTypeIndirect
			}
		}
		if err := addRouteRow(&row); err != nil {
			// 上次失败尝试可能已残留同一条 /32 路由（The object already exists）——
			// 视为成功并纳入管理，保证后续 removeHostRoutes 能清理它
			if syscall.Errno(5010) != err {
				vlog("addRouteRow(%s/32) failed: %v", ip, err)
				lastAddErr = fmt.Errorf("add route %s/32 via %s ifIdx=%d: %v", ip, dwordToIP(base.NextHop), base.IfIndex, err)
				continue
			}
		}
		hostRoutes = append(hostRoutes, row)
	}
	if len(hostRoutes) == 0 {
		rows, terr := getIpForwardTable()
		br, brok := getBestRoute(nodeIPs[0])
		if lastAddErr != nil {
			return hostRoutes, 0, fmt.Errorf("failed to write direct host route for %s: %v", nodeIPs[0], lastAddErr)
		}
		return hostRoutes, 0, fmt.Errorf("failed to write direct host route: system route to %s not found (diag: tableRows=%d tableErr=%v getBestRoute ok=%v ifIdx=%d nextHop=%s ips=%v)",
			nodeIPs[0], len(rows), terr, brok, br.IfIndex, dwordToIP(br.NextHop), nodeIPs)
	}

	// 4) 按当前策略写分流路由（全部指向 TUN 网关；策略由 sstap.go 的规则引擎计算）
	routes, polErr := sstapPolicyRoutes(policy)
	if polErr != nil {
		return nil, 0, polErr
	}
	var firstErr error
	for _, r := range routes {
		if err := addRoute2(tunIdx, r, gw, routeMetric); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		routed++
	}
	return hostRoutes, routed, firstErr
}

// removeHostRoutes 回收写入物理网卡的节点 /32 直连路由，返回成功删除的条数
func removeHostRoutes(routes *[]mibIPForwardRow) int {
	if routes == nil || len(*routes) == 0 {
		return 0
	}
	n := 0
	for _, r := range *routes {
		row := mibIPForwardRow{
			Dest:    r.Dest,
			Mask:    r.Mask,
			NextHop: r.NextHop,
			IfIndex: r.IfIndex,
		}
		if deleteRouteRow(&row) == nil {
			n++
		}
	}
	*routes = nil
	return n
}

// addTunIPv6Route 把 IPv6 全局单播 2000::/3 送进虚拟网卡，堵住 IPv6 泄漏。
// 旧版 IP Helper 路由 API（CreateIpForwardEntry）不支持 IPv6，因此与
// 网卡 metric/DNS 一样走 PowerShell 的 New-NetRoute（ActiveStore 不持久化，重启自动消失）。
// 只接管 2000::/3 而不是 ::/0：ULA (fc00::/7) 与链路本地 (fe80::/10) 保持系统原有行为。
func addTunIPv6Route(ifIdx uint32) error {
	ps := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf("New-NetRoute -InterfaceIndex %d -DestinationPrefix '2000::/3' -NextHop '%s' -RouteMetric %d -PolicyStore ActiveStore -ErrorAction Stop | Out-Null",
			ifIdx, tunGateway6, routeMetric))
	ps.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if out, err := ps.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ------------------------- 生命周期 -------------------------

// tunTrafficSample 通过 Windows IP Helper 采样 TUN 网卡流量（简易/全局模式下的真实速率）
func (a *App) tunTrafficSample() (up, down int64, ok bool) {
	a.mu.RLock()
	idx := a.tunIfaceIdx
	running := a.tunRunning
	a.mu.RUnlock()
	if !running || idx == 0 {
		return 0, 0, false
	}
	row := windows.MibIfRow2{}
	row.InterfaceIndex = idx
	if err := windows.GetIfEntry2Ex(0, &row); err != nil {
		return 0, 0, false
	}
	return int64(row.OutOctets), int64(row.InOctets), true
}
