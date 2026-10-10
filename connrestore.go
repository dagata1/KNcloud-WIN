package main

import (
	"fmt"
	"time"

	"v2rayN-win11/internal/connstate"
)

// ------------------------- 启动时恢复上次的连接状态 -------------------------
//
// 上次的连接状态（断开 / 仅内核 / 系统代理 / TUN）随每次 savePersisted 记入 config.json 的
// lastConn。启动（包括开机自启）时：
//   - proxy：开内核 + 系统代理（旧配置没有记录时也按这个，和旧版本一致）；
//   - core：只开内核，系统代理保持关闭；
//   - tun：开内核 + 系统代理，再按退避节奏恢复 TUN（开机时网络 / 网卡可能还没就绪）；
//   - off（旧版本记下的「断开」）：内核常开，照样开内核，只是不开系统代理（同 core）。
// 没有节点时内核以直连配置启动；节点配置起不来时降级直连 + 自动重试（见 corefallback.go）；
// 连直连配置都起不来就弹窗报错并退出程序；
// 开机自启后的前几分钟用更长的重试表。

// rememberConnStateLocked 计算并返回要持久化的连接状态（调用方持有 a.mu）。
// 退出清理会先停内核 / 撤代理再落盘，那时的「断开」不是用户意图，不覆盖；
// 启动恢复完成前也不覆盖（那时内核还没起来）。
func (a *App) rememberConnStateLocked() string {
	if !a.connRestored.Load() || a.quitting.Load() || a.cleaned.Load() {
		return a.lastConn
	}
	a.lastConn = connstate.Current(a.lastConn, connstate.Snapshot{
		TunRunning:      a.tunRunning,
		TunWanted:       a.tunWanted,
		CoreRunning:     a.coreRunning,
		CoreFailed:      !a.coreRunning && (a.coreErr != "" || a.coreRetrying),
		SystemProxy:     a.systemProxy,
		SysProxyPending: a.sysProxyPending,
	})
	return a.lastConn
}

// coreRetrySchedule 内核自动重试的等待表：开机自启后 5 分钟内用更耐心的表。
func (a *App) coreRetrySchedule() []time.Duration {
	if a.startHidden && time.Since(a.startedAt) < 5*time.Minute {
		return connstate.BootCoreRetrySchedule
	}
	return coreRetryBackoff
}

// restoreConnectionOnStartup 按上次的连接状态自动连接。
// a.mu 只在改内核状态时持有；写注册表 / 通知 WinINet 放到锁外，避免启动期间
// 所有界面绑定（GetNodes、SelectNode……）和退出都排队等这把锁。
func (a *App) restoreConnectionOnStartup() {
	a.mu.Lock()
	state := connstate.Normalize(a.lastConn)
	plan := connstate.PlanFor(state)
	a.addLogInternal("info", fmt.Sprintf("Restoring last connection state: %s", state))
	// 内核常开：PlanFor 对任何状态都给 StartCore（off 只是不开系统代理）
	a.tunWanted = plan.Tun
	if err := a.startCoreLocked(); err != nil {
		a.coreRunning = false
		a.addLogInternal("error", fmt.Sprintf("Auto-start core failed: %v", err))
		if plan.SystemProxy {
			a.sysProxyPending = true // 启动本应开启系统代理：内核（含直连兜底）起来后补上
		}
		a.handleCoreStartFailureLocked(err, true)
		if !a.coreRunning && !a.exiting() {
			// 连直连兜底都起不来：直接报错退出，不在后台空转
			a.mu.Unlock()
			a.startupCoreFatal(err)
			return
		}
		a.connRestored.Store(true)
		a.savePersisted()
		a.mu.Unlock()
		a.emitRefresh()
		if plan.Tun {
			go a.restoreTunLoop() // TUN 启动会自己拉起内核，不必等内核重试
		}
		return
	}
	a.coreRunning = true
	a.markCoreRunningLocked(false)
	server := fmt.Sprintf("127.0.0.1:%d", a.settings.HttpPort)
	a.mu.Unlock()

	if plan.SystemProxy && !a.quitting.Load() {
		err := setWindowsSystemProxy(true, server)
		a.mu.Lock()
		if err != nil {
			a.addLogInternal("error", fmt.Sprintf("Failed to auto-enable system proxy: %v", err))
		} else if a.quitting.Load() || a.cleaned.Load() {
			// 退出清理已经跑过：别把系统代理留在开启状态
			setWindowsSystemProxy(false, "")
		} else {
			a.systemProxy = true
			a.addLogInternal("info", fmt.Sprintf("System proxy auto-enabled -> %s", server))
		}
	} else {
		a.mu.Lock()
		if !plan.SystemProxy {
			a.addLogInternal("info", "System proxy left off, as in the last session")
		}
	}
	a.connRestored.Store(true)
	a.savePersisted()
	a.mu.Unlock()
	tray.requestRebuild()

	if plan.Tun {
		go a.restoreTunLoop()
	}
}

// restoreTunLoop 开机恢复 TUN：按 connstate.TunRestoreSchedule 反复尝试（网络没就绪时
// 解析节点 / 找物理出口会失败，预校验阶段失败不会动系统设置）。用户在此期间手动改了
// 连接方式（开关 TUN、选策略、开关内核）或退出程序，立即停止。
func (a *App) restoreTunLoop() {
	canceled := func() bool { return a.quitting.Load() || a.cleaned.Load() }
	schedule := connstate.TunRestoreSchedule
	var lastErr error
	for i, wait := range schedule {
		deadline := time.Now().Add(wait)
		for time.Now().Before(deadline) {
			if canceled() {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !a.lockWithin(15*time.Second, canceled) {
			if canceled() {
				return
			}
			continue // 锁被长操作占着：算一次失败，继续退避
		}
		if canceled() || !a.tunWanted || a.tunRunning {
			a.mu.Unlock()
			return
		}
		err := a.tunStartLocked()
		if err == nil {
			a.tunWanted = false
			a.addLogInternal("info", fmt.Sprintf("TUN restored from last session (attempt %d)", i+1))
			a.savePersisted()
			a.mu.Unlock()
			a.emitRefresh()
			return
		}
		lastErr = err
		a.mu.Unlock()
		a.addLogInternal("warn", fmt.Sprintf("Restore TUN attempt %d/%d failed: %v", i+1, len(schedule), err))
	}
	// 全部失败：保持系统代理模式可用；tunWanted 保留，下次启动仍会尝试恢复 TUN
	a.addLogInternal("error", fmt.Sprintf("Could not restore TUN after %d attempts (%v); staying in system-proxy mode", len(schedule), lastErr))
	a.emitToast("TUN 自动恢复失败，已保持系统代理模式。可在主界面手动重新开启 TUN", "error")
	a.emitRefresh()
}
