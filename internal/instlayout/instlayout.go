// Package instlayout 判定程序目录的用法：绿色版（数据放 exe 目录）还是安装版（数据放 %APPDATA%\KNcloud），
// 以及应用内更新能否原地替换 exe 目录里的文件。纯标准库，可在 Linux 上跑单测。
//
//	绿色版：exe 目录可写 → 数据根 = 程序根 = exe 目录（configs\、logs\ 在 exe 旁边）。
//	安装版：exe 目录里有安装器写入的标记文件 MarkerName → 数据根为空（调用方用 %APPDATA%\KNcloud，
//	        卸载时保留），程序根 = exe 目录（程序是 requireAdministrator，Program Files 可写，
//	        应用内更新照常替换 KNcloud.exe 与 bin\）。
//	都不满足（目录不可写）：两者都为空 —— 数据用 %APPDATA%\KNcloud，不支持应用内更新。
package instlayout

import (
	"os"
	"path/filepath"
)

// MarkerName 安装器（build/windows/installer/project.nsi）写在安装目录里的标记文件名。
const MarkerName = "KNcloud.installed"

// IsInstalled exe 目录里是否有安装版标记。
func IsInstalled(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, MarkerName))
	return err == nil && !st.IsDir()
}

// writable 能否在 dir 下（sub 非空时先建 dir\sub）写文件。
func writable(dir, sub string) bool {
	d := dir
	if sub != "" {
		d = filepath.Join(dir, sub)
		if err := os.MkdirAll(d, 0755); err != nil {
			return false
		}
	}
	probe := filepath.Join(d, ".write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0644); err != nil {
		return false
	}
	os.Remove(probe)
	return true
}

// Resolve 返回 dataRoot（空 = 用 %APPDATA%\KNcloud）与 appRoot（空 = 不能应用内更新）。
// 绿色版判定会在 exe 目录下建 configs\（与旧逻辑一致）；安装版不会在安装目录里建任何数据目录。
func Resolve(exeDir string) (dataRoot, appRoot string) {
	if exeDir == "" {
		return "", ""
	}
	if IsInstalled(exeDir) {
		if writable(exeDir, "") {
			return "", exeDir
		}
		return "", ""
	}
	if writable(exeDir, "configs") {
		return exeDir, exeDir
	}
	return "", ""
}
