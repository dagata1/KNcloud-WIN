package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"v2rayN-win11/internal/autostart"
)

// 开机自启改用计划任务实现（见 internal/autostart 的包注释）：
// 程序清单是 requireAdministrator，Windows 登录时会静默跳过 HKCU\...\Run 里需要提权的程序，
// 旧版本写的 Run 键因此从来没生效过。现在：
//   - 开启：创建 / 覆盖计划任务 \KNcloud-WIN（登录时触发、最高权限、带 --autostart 参数）；
//   - 关闭：删除该任务；
//   - 两种情况都清理旧版本留下的 Run 键值 KNcloud-WIN / KNcloud。

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "KNcloud-WIN"
	// legacyRunValueName 是改名前用的自启项名。
	legacyRunValueName = "KNcloud"

	schtasksTimeout = 20 * time.Second

	// autostartTaskName 计划任务名，「任务计划程序」根目录下可见。
	autostartTaskName = autostart.TaskName
)

var (
	errNoExecutablePath = errors.New("cannot resolve current executable path")
	errTaskNotFound     = errors.New("scheduled task not found")
	// errLegacyCleanup 计划任务已处理成功，只是清理旧 Run 键失败：调用方记警告即可，不必回滚设置。
	errLegacyCleanup = errors.New("清理旧的注册表自启项失败")
)

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

// schtasksPath 优先用系统目录里的 schtasks.exe，避免被当前目录 / PATH 里的同名程序劫持。
func schtasksPath() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		p := filepath.Join(root, "System32", "schtasks.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "schtasks.exe"
}

// decodeConsoleOutput 把 schtasks 的输出转成 UTF-8：重定向时它按 OEM 代码页（中文系统为 GBK）输出。
func decodeConsoleOutput(b []byte) string {
	if len(b) == 0 || utf8.Valid(b) {
		return string(b)
	}
	const cpOEM = 1 // CP_OEMCP
	n, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return string(b)
	}
	buf := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), &buf[0], n); err != nil {
		return string(b)
	}
	return windows.UTF16ToString(buf)
}

// runSchtasks 隐藏窗口运行 schtasks.exe（CREATE_NO_WINDOW，不闪黑框），返回合并后的输出。
func runSchtasks(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), schtasksTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, schtasksPath(), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	raw, err := cmd.CombinedOutput()
	out := strings.TrimSpace(decodeConsoleOutput(raw))
	if err != nil {
		if out != "" {
			return out, fmt.Errorf("schtasks %s: %w: %s", args[0], err, out)
		}
		return out, fmt.Errorf("schtasks %s: %w", args[0], err)
	}
	return out, nil
}

// taskFilePath 计划任务在磁盘上的定义文件（UTF-16LE XML），管理员可读。
func taskFilePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "Tasks", autostart.TaskName)
}

// readTaskXML 读取已注册任务的 XML：先直接读任务文件，读不了再问 schtasks。
func readTaskXML() ([]byte, error) {
	data, err := os.ReadFile(taskFilePath())
	if err == nil {
		return data, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, errTaskNotFound
	}
	out, qerr := runSchtasks("/Query", "/TN", autostart.TaskName, "/XML", "ONE")
	if qerr != nil {
		// 不存在时 schtasks 也返回非零；输出是本地化文本，无法可靠区分，统一按不存在处理
		return nil, errTaskNotFound
	}
	return []byte(out), nil
}

// autoStartTaskExists 计划任务是否存在（不关心指向哪里）。
func autoStartTaskExists() bool {
	if _, err := os.Stat(taskFilePath()); err == nil {
		return true
	} else if errors.Is(err, os.ErrNotExist) {
		return false
	}
	_, err := runSchtasks("/Query", "/TN", autostart.TaskName)
	return err == nil
}

// isAutoStartEnabled 检查自启计划任务是否存在、已启用、最高权限且指向当前 exe（带 --autostart）。
func isAutoStartEnabled() bool {
	exe, err := currentExecutablePath()
	if err != nil {
		return false
	}
	data, err := readTaskXML()
	if err != nil {
		return false
	}
	info, err := autostart.ParseTaskXML(data)
	if err != nil {
		return false
	}
	return info.PointsTo(exe, os.LookupEnv)
}

// currentUserSID 当前进程用户的 SID。用 SID 而不是 DOMAIN\user：用户名含中文 / 改过名也不受影响。
func currentUserSID() (string, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return tu.User.Sid.String(), nil
}

// createAutoStartTask 创建或覆盖（/F）自启计划任务，指向当前 exe。
func createAutoStartTask() error {
	exe, err := currentExecutablePath()
	if err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("获取当前用户 SID 失败: %w", err)
	}
	xmlText, err := autostart.BuildTaskXML(autostart.TaskConfig{
		UserID:           sid,
		Command:          exe,
		Arguments:        autostart.Arg,
		WorkingDirectory: filepath.Dir(exe),
		Delay:            autostart.DefaultDelay,
	})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "kncloud-autostart-*.xml")
	if err != nil {
		return fmt.Errorf("写入任务定义临时文件失败: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(autostart.EncodeUTF16LE(xmlText)); err != nil {
		f.Close()
		return fmt.Errorf("写入任务定义临时文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("写入任务定义临时文件失败: %w", err)
	}
	if _, err := runSchtasks("/Create", "/TN", autostart.TaskName, "/XML", tmp, "/F"); err != nil {
		return fmt.Errorf("创建开机自启计划任务失败: %w", err)
	}
	return nil
}

// deleteAutoStartTask 删除自启计划任务；本来就不存在视为成功。
func deleteAutoStartTask() error {
	if !autoStartTaskExists() {
		return nil
	}
	if _, err := runSchtasks("/Delete", "/TN", autostart.TaskName, "/F"); err != nil {
		return fmt.Errorf("删除开机自启计划任务失败: %w", err)
	}
	return nil
}

// legacyRunValuesExist 旧版本写的 HKCU Run 自启项是否还在。
func legacyRunValuesExist() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	for _, name := range []string{runValueName, legacyRunValueName} {
		if _, _, err := k.GetValue(name, nil); err == nil || errors.Is(err, registry.ErrShortBuffer) {
			return true
		}
	}
	return false
}

// removeLegacyRunValues 删除旧版本写的 HKCU Run 自启项（不存在时忽略）。
func removeLegacyRunValues() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	var errs []error
	for _, name := range []string{runValueName, legacyRunValueName} {
		if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// setAutoStart 开启时创建 / 更新计划任务（换了安装路径会覆盖成新路径），关闭时删除任务；
// 两种情况都清理旧的 Run 键值。任务操作失败返回普通 error（设置应回滚）；
// 只有旧 Run 键清理失败时返回包裹 errLegacyCleanup 的 error（任务本身已经生效）。
func setAutoStart(enable bool) error {
	var taskErr error
	if enable {
		taskErr = createAutoStartTask()
	} else {
		taskErr = deleteAutoStartTask()
	}
	cleanErr := removeLegacyRunValues()
	if taskErr != nil {
		return taskErr
	}
	if cleanErr != nil {
		return fmt.Errorf("%w: %v", errLegacyCleanup, cleanErr)
	}
	return nil
}

// syncAutoStartOnStartup 启动时按设置校准自启状态，尽量不起 schtasks 进程：
// 任务已正确指向当前 exe 且没有旧 Run 键时什么都不做。返回 changed=true 表示做了创建 / 迁移 / 删除。
func syncAutoStartOnStartup(enable bool) (changed bool, err error) {
	legacy := legacyRunValuesExist()
	if enable {
		if isAutoStartEnabled() && !legacy {
			return false, nil
		}
		return true, setAutoStart(true)
	}
	if !autoStartTaskExists() && !legacy {
		return false, nil
	}
	return true, setAutoStart(false)
}
