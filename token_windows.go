package main

// 账户凭证保护：V2Board 登录 token 用 Windows DPAPI（CryptProtectData）加密后落盘。
// DPAPI 绑定「当前用户 + 当前机器」：配置文件被拷到别的机器/别的账户下无法解密，
// 拿到 config.json 不等于拿到登录凭证。兼容旧版明文 token：读到未加密格式时原样
// 使用并在下次保存时转为密文。

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tokenCipherPrefix 加密 token 的前缀标识（dpapi:<base64>）。
const tokenCipherPrefix = "dpapi:"

// protectToken 用 DPAPI 加密 token，返回 "dpapi:<base64>"；空串原样返回。
func protectToken(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	src := []byte(plain)
	name, err := windows.UTF16PtrFromString("KNcloud-WIN auth token")
	if err != nil {
		return "", err
	}
	out := &windows.DataBlob{}
	if err := windows.CryptProtectData(
		&windows.DataBlob{Size: uint32(len(src)), Data: &src[0]},
		name,
		nil,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		out,
	); err != nil {
		return "", fmt.Errorf("CryptProtectData: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	enc := make([]byte, out.Size)
	copy(enc, unsafe.Slice(out.Data, out.Size))
	return tokenCipherPrefix + base64.StdEncoding.EncodeToString(enc), nil
}

// unprotectToken 解开 DPAPI 加密的 token；明文（旧版）原样返回；
// 是密文但解密失败时返回 ok=false。
func unprotectToken(stored string) (plain string, ok bool) {
	if stored == "" {
		return "", true
	}
	if !strings.HasPrefix(stored, tokenCipherPrefix) {
		// 旧版本明文 token：原样返回，下次保存时转为密文
		return stored, true
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len(tokenCipherPrefix):])
	if err != nil || len(raw) == 0 {
		return "", false
	}
	var desc *uint16
	out := &windows.DataBlob{}
	if err := windows.CryptUnprotectData(
		&windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]},
		&desc,
		nil,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		out,
	); err != nil {
		return "", false
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	plainBytes := make([]byte, out.Size)
	copy(plainBytes, unsafe.Slice(out.Data, out.Size))
	return string(plainBytes), true
}

// encryptStoredToken 供 savePersisted 调用：token 加密后写入配置。
func encryptStoredToken(a *AccountInfo) {
	if a == nil {
		return
	}
	if enc, err := protectToken(a.AuthToken); err == nil {
		a.AuthToken = enc
	}
	// DPAPI 失败极罕见（用户配置文件损坏）：保留明文以保证功能可用
}

// decryptStoredToken 供 loadPersisted 调用：兼容明文/密文；解密失败视为未登录。
func decryptStoredToken(a *AccountInfo) {
	if a == nil {
		return
	}
	plain, ok := unprotectToken(a.AuthToken)
	if !ok {
		// 密文换机器/换用户后无法解密：清掉凭证，要求重新登录
		a.LoggedIn = false
		a.AuthToken = ""
		return
	}
	a.AuthToken = plain
}
