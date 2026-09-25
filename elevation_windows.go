package main

// TUN 按需提权：应用清单为 asInvoker（普通权限启动，不再每次开机弹 UAC），
// 只在用户真正开启 TUN 模式时请求管理员权限 —— 通过 ShellExecute(runas)
// 以 --tun-autostart 参数重启自身，新实例完成初始化后自动拉起 TUN。

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// errRelaunchRequested 提权重启已获 UAC 同意、旧实例正在退出时返回给调用方的提示。
var errRelaunchRequested = errors.New("已请求以管理员身份重启应用以开启 TUN 模式")

var shell32 = windows.NewLazySystemDLL("shell32.dll")
var procShellExecuteW = shell32.NewProc("ShellExecuteW")

// relaunchElevatedForTun 请求提权重启并在新实例中自动开启 TUN。
// UAC 被拒绝时返回普通 error（调用方向用户展示）；
// UAC 通过后本实例在后台协程中清理并退出，返回 errRelaunchRequested。
//
// 注意单实例锁时序：旧实例在 ShellExecute 返回后数毫秒内退出，而新实例从
// 进程创建到获取单实例锁需要数百毫秒（Go 运行时 + 配置加载 + wails 初始化），
// 正常情况下不会发生「新实例被当作第二次启动」的竞态。
func relaunchElevatedForTun(a *App) error {
	exe, err := currentExecutablePath()
	if err != nil {
		return fmt.Errorf("cannot resolve executable for elevation: %w", err)
	}
	verb, e1 := windows.UTF16PtrFromString("runas")
	file, e2 := windows.UTF16PtrFromString(exe)
	params, e3 := windows.UTF16PtrFromString("--tun-autostart")
	if e1 != nil || e2 != nil || e3 != nil {
		return errors.New("cannot build relaunch arguments")
	}
	const swShownormal = 1
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		0,
		swShownormal,
	)
	// ShellExecuteW 返回值 > 32 表示成功
	if ret <= 32 {
		return fmt.Errorf("需要管理员权限才能开启 TUN 模式（提权请求被取消或失败，代码 %d）", ret)
	}
	a.addLogInternal("info", "UAC granted, restarting with administrator privileges for TUN mode")
	go func() {
		// SimpleConnect 仍持有 a.mu，这里等它返回后再清理退出
		a.cleanup()
		os.Exit(0)
	}()
	return errRelaunchRequested
}
