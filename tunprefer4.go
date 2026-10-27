package main

import (
	"fmt"
	"strings"
)

// TUN 没接管 IPv6 时（原生 tun2socks 引擎只给网卡配 IPv4、或路由形态不是全局默认路由，
// 都不会装 2000::/3 防泄漏路由），系统解析到的 AAAA 会经物理网卡直连目标、绕过代理，
// 表现为「在 TUN 下访问的是本机 IPv6」（例：NodeSeek 关闭 IPv6 后直接拦截页面）。
//
// 这期间把 IPv4 映射前缀 ::ffff:0:0/96 的优先级临时调到最高（RFC 6724 策略表），
// 让 getaddrinfo / 浏览器的地址排序优先选 IPv4，IPv4 连接进 TUN 走代理。
// 只写 store=active：即使客户端异常退出，重启系统也会恢复；正常关 TUN 时按原值写回。
const (
	v4MappedPrefix   = "::ffff:0:0/96"
	prefer4Prec      = "100" // 高于 ::1/128 的 50 与 ::/0 的 40
	defaultV4MapPrec = "35"
	defaultV4MapLbl  = "4"
)

// parseV4MappedPolicy 从 `netsh interface ipv6 show prefixpolicies` 输出取 ::ffff:0:0/96 的
// 优先级与标签；没有该条目时 ok=false。
func parseV4MappedPolicy(out string) (prec, label string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && strings.EqualFold(f[2], v4MappedPrefix) {
			return f[0], f[1], true
		}
	}
	return "", "", false
}

func setV4MappedPolicy(prec, label string) error {
	out, err := runHidden("netsh", "interface", "ipv6", "set", "prefixpolicy", v4MappedPrefix, prec, label, "store=active")
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "ok") {
		// 条目被删过时 set 会失败，改用 add
		out2, err2 := runHidden("netsh", "interface", "ipv6", "add", "prefixpolicy", v4MappedPrefix, prec, label, "store=active")
		if err2 != nil {
			return fmt.Errorf("netsh prefixpolicy: %v: %s / %s", err2, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	return nil
}

// tunPreferIPv4Locked TUN 未接管 IPv6 时开启「系统优先 IPv4」，记住原值以便恢复。
func (a *App) tunPreferIPv4Locked() {
	if a.tunPrefer4 {
		return
	}
	prec, label := defaultV4MapPrec, defaultV4MapLbl
	if out, err := runHidden("netsh", "interface", "ipv6", "show", "prefixpolicies", "store=active"); err == nil {
		if p, l, ok := parseV4MappedPolicy(string(out)); ok {
			prec, label = p, l
		}
	}
	if prec == prefer4Prec {
		// 上次异常退出留下的值：恢复时回到系统默认
		prec = defaultV4MapPrec
	}
	if err := setV4MappedPolicy(prefer4Prec, label); err != nil {
		a.addLogInternal("warn", fmt.Sprintf("TUN: IPv6 not captured and prefer-IPv4 policy failed, IPv6 sites may bypass the proxy: %v", err))
		return
	}
	a.tunPrefer4 = true
	a.tunPrefer4Orig = [2]string{prec, label}
	a.addLogInternal("info", "TUN: IPv6 not captured by this engine, system set to prefer IPv4 so traffic stays in the tunnel")
}

// tunRestoreIPv4PrefLocked 关 TUN 时恢复地址选择策略。
func (a *App) tunRestoreIPv4PrefLocked() {
	if !a.tunPrefer4 {
		return
	}
	a.tunPrefer4 = false
	if err := setV4MappedPolicy(a.tunPrefer4Orig[0], a.tunPrefer4Orig[1]); err != nil {
		a.addLogInternal("warn", fmt.Sprintf("TUN: failed to restore IPv4/IPv6 preference (resets on reboot): %v", err))
	}
}
