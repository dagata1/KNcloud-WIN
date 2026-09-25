package main

// 开机自启。按运行权限选择机制：
//   - 管理员权限运行（TUN 场景）：注册任务计划程序任务（Logon 触发 + RunLevel Highest），
//     登录时静默以管理员启动 —— HKCU Run 键对 requireAdministrator 程序永远无效，
//     而 asInvoker 清单下计划任务是「登录即静默提权」的唯一正解；
//   - 普通权限运行：写 HKCU Run 键（asInvoker 清单下登录即启动，无 UAC）。
// 检测自启状态时两处都查，避免换机制后状态误判。

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "KNcloud-WIN"
	// legacyRunValueName 是改名前用的自启项名。改名后旧条目仍指向旧的 exe 路径，
	// 会在任务管理器里留下一条永远启动失败的死项，所以写新值时顺手清掉。
	legacyRunValueName = "KNcloud"

	taskName = "KNcloud-WIN"
)

var errNoExecutablePath = errors.New("cannot resolve current executable path")

// currentExecutablePath 返回当前进程的可执行文件绝对路径（解析软链接后）。
func currentExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if exe == "" {
		return "", errNoExecutablePath
	}
	return exe, nil
}

// runPowershell 执行一条 PowerShell 命令（隐藏窗口），返回合并输出。
func runPowershell(cmd string) (string, error) {
	ps := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd)
	ps.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := ps.CombinedOutput()
	return string(out), err
}

// isAutoStartEnabled 检查当前用户登录自启是否已配置（任务计划或 Run 键任一存在即视为已启用）。
func isAutoStartEnabled() bool {
	if out, err := runPowershell(fmt.Sprintf(
		"if (Get-ScheduledTask -TaskName '%s' -ErrorAction SilentlyContinue) { 'YES' }", taskName)); err == nil && strings.Contains(out, "YES") {
		return true
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue(runValueName)
	if err != nil {
		return false
	}
	return val != ""
}

// setAutoStart 写入 / 移除开机自启。返回 error 时保持原状（尽力而为）。
func setAutoStart(enable bool) error {
	if !enable {
		// 两套机制都清理，避免残留
		_, _ = runPowershell(fmt.Sprintf(
			"Unregister-ScheduledTask -TaskName '%s' -Confirm:$false -ErrorAction SilentlyContinue", taskName))
		return removeRunKeyValue()
	}

	exe, err := currentExecutablePath()
	if err != nil {
		return err
	}

	if isElevated() {
		// 管理员：任务计划程序（登录静默提权启动）
		ps := fmt.Sprintf(
			"$action = New-ScheduledTaskAction -Execute '%s'; "+
				"$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME; "+
				"$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -RunLevel Highest; "+
				"Register-ScheduledTask -TaskName '%s' -Action $action -Trigger $trigger -Principal $principal -Force | Out-Null",
			strings.ReplaceAll(exe, "'", "''"), taskName)
		if out, err := runPowershell(ps); err != nil {
			return fmt.Errorf("register scheduled task: %v: %s", err, strings.TrimSpace(out))
		}
		// 顺手清掉可能存在的旧 Run 键，避免双自启
		_ = removeRunKeyValue()
		return nil
	}

	// 普通权限：HKCU Run 键。清掉旧的任务计划（非管理员可能无权删除，尽力而为）
	_, _ = runPowershell(fmt.Sprintf(
		"Unregister-ScheduledTask -TaskName '%s' -Confirm:$false -ErrorAction SilentlyContinue", taskName))
	return setRunKeyValue(exe)
}

func setRunKeyValue(exe string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// 清理改名前的自启项残留（不存在时 registry.ErrNotExist，忽略即可）。
	if err := k.DeleteValue(legacyRunValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return k.SetStringValue(runValueName, `"`+exe+`"`)
}

func removeRunKeyValue() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for _, name := range []string{runValueName, legacyRunValueName} {
		if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return nil
}
