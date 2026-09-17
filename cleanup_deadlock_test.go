package main

import (
	"testing"
	"time"
)

// 退出路径死锁回归测试。
//
// 背景：a.mu 是 sync.RWMutex，不可重入。cleanup() 会持写锁清理各子系统，
// 而它曾经直接调用 stopWebLogin() —— 后者自己也要 a.mu.Lock()，于是必然自锁。
//
// 触发路径（两条都是日常操作，不是边缘情况）：
//
//	托盘「退出」 -> quitApp() -> runtime.Quit() -> beforeClose() -> cleanup() -> 卡死
//	关闭窗口（未开启「最小化到托盘」）-> beforeClose() -> cleanup() -> 卡死
//
// 危害不只是「退不掉」。看 cleanup() 里的语句顺序：还原系统代理排在 stopWebLogin
// 之前，停内核 / 停 TUN / 存盘排在它之后。所以死锁时用户上网看着是正常的
// （代理已还原），但：
//   - stopCoreLocked()   没执行 -> Xray 不退，端口不放
//   - tunSoftStopLocked() 没执行 -> TUN 分流路由残留在系统路由表里
//   - savePersisted()     没执行 -> 本次会话的节点/设置/流量统计全部丢失
//
// 最后只能任务管理器强杀。
//
// 修复：cleanup() 改调 stopWebLoginLocked()（假定调用方已持锁的变体）。
//
// 本测试用超时来断言 —— 死锁的表现就是「永远不返回」，只能靠 timeout 捕获。
// 跑在修复前的代码上必定超时失败。
func TestCleanupDoesNotDeadlock(t *testing.T) {
	// webLogin 为 nil 同样会死锁：旧实现是先 Lock 再判空，
	// 所以「从未用过网页登录」的用户一样会中招。这里特意不初始化它。
	app := &App{
		settings:      AppSettings{SocksPort: 10808, HttpPort: 10809},
		nodes:         []NodeItem{},
		subscriptions: []SubscriptionItem{},
		// systemProxy 保持 false：避免测试真的去改本机注册表
		systemProxy: false,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.cleanup()
	}()

	select {
	case <-done:
		// 正常返回即通过
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup() 未在 10 秒内返回：退出路径出现自锁（a.mu 不可重入）。" +
			"检查 cleanup() 是否又调用了会自行加 a.mu 的函数，" +
			"持锁调用必须走 *Locked 变体。")
	}

	// cleanup 必须可重入调用：beforeClose 与 quitApp 都可能触发它
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		app.cleanup()
	}()
	select {
	case <-done2:
	case <-time.After(10 * time.Second):
		t.Fatal("第二次 cleanup() 卡死")
	}
}

// TestStopWebLoginVariantsAgree 验证两个变体行为一致：
// 不持锁的 stopWebLogin 与持锁的 stopWebLoginLocked 都必须能安全处理 nil manager。
func TestStopWebLoginVariantsAgree(t *testing.T) {
	// nil manager
	app := &App{}
	app.stopWebLogin()
	app.mu.Lock()
	app.stopWebLoginLocked()
	app.mu.Unlock()

	// 非 nil manager（未启动服务，字段均为零值）
	app2 := &App{webLogin: &webLoginManager{}}
	app2.stopWebLogin()
	app2.mu.Lock()
	app2.stopWebLoginLocked()
	app2.mu.Unlock()
}
