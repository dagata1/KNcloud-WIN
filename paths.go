package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"v2rayN-win11/internal/instlayout"
)

// 绿色版目录布局（与 v2rayN 类似，整个文件夹可随意拷贝）：
//
//	KNcloud\
//	  KNcloud.exe
//	  bin\       geoip.dat、geosite.dat、wintun.dll、badvpn-tun2socks.exe 等运行资源
//	  configs\   config.json（账号、订阅、设置）
//	  logs\      运行日志
//
// exe 所在目录不可写，或是 Setup.exe 安装版（目录里有 KNcloud.installed 标记）时，
// 配置和日志放在 %APPDATA%\KNcloud（安装版卸载时保留）。

var (
	dataRootOnce sync.Once
	dataRoot     string // 绿色版根目录（exe 目录）；空表示用 %APPDATA%\KNcloud
	appRoot      string // 程序根目录（应用内更新原地替换文件用）；空表示不支持在线更新
)

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// legacyConfigDir 是旧版（单 exe）与安装版使用的 %APPDATA%\KNcloud。
func legacyConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		base, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(base, "AppData", "Roaming")
	}
	return filepath.Join(base, "KNcloud"), nil
}

func resolveRoots() {
	dataRootOnce.Do(func() {
		if os.Getenv("KNCLOUD_NO_PORTABLE") != "" {
			return
		}
		dir := exeDir()
		if dir == "" || isGoTestBinary() {
			return
		}
		// 安装版（目录里有安装器写的 KNcloud.installed）：数据放 %APPDATA%\KNcloud（卸载时保留），
		// 程序目录只用于应用内更新；绿色版：数据和程序都在 exe 目录。
		dataRoot, appRoot = instlayout.Resolve(dir)
		if dataRoot != "" {
			migrateLegacyConfig(filepath.Join(dataRoot, "configs"))
		}
	})
}

// portableRoot 绿色版数据根目录（exe 目录能创建 configs 且可写，且不是安装版）；空表示用 %APPDATA%\KNcloud。
// go test 产生的临时测试程序不算（避免测试往临时目录乱写或吃掉真实配置）。
func portableRoot() string {
	resolveRoots()
	return dataRoot
}

// updateRoot 应用内更新替换文件的程序根目录：绿色版与安装版（Program Files，程序以管理员运行可写）
// 都是 exe 目录；目录不可写时为空（不支持在线更新）。
func updateRoot() string {
	resolveRoots()
	return appRoot
}

func isGoTestBinary() bool {
	if len(os.Args) == 0 {
		return false
	}
	n := strings.ToLower(filepath.Base(os.Args[0]))
	return strings.HasSuffix(n, ".test") || strings.HasSuffix(n, ".test.exe")
}

// migrateLegacyConfig 绿色版首次启动时，把旧版 %APPDATA%\KNcloud\config.json 复制过来，
// 账号、订阅和设置不丢。只复制、不删除旧文件，回退旧版也能用。
func migrateLegacyConfig(cfgDir string) {
	dst := filepath.Join(cfgDir, "config.json")
	if _, err := os.Stat(dst); err == nil {
		return
	}
	old, err := legacyConfigDir()
	if err != nil {
		return
	}
	_ = copyFile(filepath.Join(old, "config.json"), dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// appConfigDir 配置目录：绿色版为 <exe目录>\configs，否则 %APPDATA%\KNcloud。
func appConfigDir() (string, error) {
	var dir string
	if root := portableRoot(); root != "" {
		dir = filepath.Join(root, "configs")
	} else {
		d, err := legacyConfigDir()
		if err != nil {
			return "", err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// appLogDir 日志目录：绿色版为 <exe目录>\logs，否则与配置目录相同。
func appLogDir() (string, error) {
	if root := portableRoot(); root != "" {
		dir := filepath.Join(root, "logs")
		if err := os.MkdirAll(dir, 0755); err == nil {
			return dir, nil
		}
	}
	return appConfigDir()
}

// resourceSearchDirs 运行资源的查找顺序：<exe目录>\bin 优先；其余是开发/测试时的源码目录。
func resourceSearchDirs(sub ...string) []string {
	var dirs []string
	if d := exeDir(); d != "" {
		dirs = append(dirs, filepath.Join(d, "bin"), d)
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(wd, "bin"))
		for _, s := range sub {
			dirs = append(dirs, filepath.Join(wd, s))
		}
	}
	return dirs
}

var errResourceMissing = errors.New("resource missing")

// findResource 在 bin 等目录里找运行资源，找不到时提示完整解压。
func findResource(name string, devSubdirs ...string) (string, error) {
	for _, d := range resourceSearchDirs(devSubdirs...) {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() > 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: 找不到 bin\\%s，请把压缩包完整解压后再运行", errResourceMissing, name)
}
