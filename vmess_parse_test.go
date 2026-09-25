package main

import (
	"encoding/base64"
	"testing"
)

func mkVMess(j string) string { return "vmess://" + base64.StdEncoding.EncodeToString([]byte(j)) }

// TestVMessPortFormats vmess JSON 无严格规范，port/aid/v 既可能是字符串
// 也可能是数字。按字符串解会整条失败，节点被静默丢弃。
func TestVMessPortFormats(t *testing.T) {
	cases := []struct {
		name, json string
		wantPort   int
		wantAid    int
	}{
		{"字符串风格(v2rayN)", `{"v":"2","ps":"A","add":"a.com","port":"443","id":"u","aid":"0","net":"tcp"}`, 443, 0},
		{"数字风格(Clash 转换)", `{"v":2,"ps":"B","add":"b.com","port":443,"id":"u","aid":0,"net":"tcp"}`, 443, 0},
		{"混合风格", `{"v":"2","ps":"C","add":"c.com","port":8443,"id":"u","aid":"64","net":"ws"}`, 8443, 64},
		{"大端口号", `{"ps":"D","add":"d.com","port":65535,"id":"u","net":"tcp"}`, 65535, 0},
	}
	for _, c := range cases {
		n, err := parseVMessLink(mkVMess(c.json))
		if err != nil {
			t.Errorf("%s: 解析失败 %v", c.name, err)
			continue
		}
		if n.Port != c.wantPort || n.AlterID != c.wantAid {
			t.Errorf("%s: port=%d aid=%d, 期望 %d/%d", c.name, n.Port, n.AlterID, c.wantPort, c.wantAid)
		}
	}
}

// TestVMessTLSFormats tls 字段常见 "tls" 字符串与 true 布尔两种写法。
func TestVMessTLSFormats(t *testing.T) {
	for _, c := range []struct{ name, json, want string }{
		{"字符串 tls", `{"ps":"A","add":"a.com","port":443,"id":"u","tls":"tls"}`, "tls"},
		{"布尔 true", `{"ps":"B","add":"b.com","port":443,"id":"u","tls":true}`, "tls"},
		{"空表示不加密", `{"ps":"C","add":"c.com","port":80,"id":"u","tls":""}`, ""},
	} {
		n, err := parseVMessLink(mkVMess(c.json))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if n.Security != c.want {
			t.Errorf("%s: security=%q, 期望 %q", c.name, n.Security, c.want)
		}
	}
}

// TestVMessFieldString 各类标量到字符串的转换，数字不得出现小数点。
func TestVMessFieldString(t *testing.T) {
	for _, c := range []struct {
		in   interface{}
		want string
	}{
		{nil, ""}, {"x", "x"}, {true, "true"}, {false, "false"},
		{float64(443), "443"}, {float64(0), "0"}, {float64(65535), "65535"},
	} {
		if got := vmessFieldString(c.in); got != c.want {
			t.Errorf("vmessFieldString(%v) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestVMessMalformedDoesNotPanic 不可信订阅内容不得导致崩溃。
func TestVMessMalformedDoesNotPanic(t *testing.T) {
	for _, l := range []string{
		"vmess://", "vmess://!!!!", mkVMess(`{`), mkVMess(`[]`), mkVMess(`null`), mkVMess(`"str"`),
		mkVMess(`{"port":{"nested":1}}`), mkVMess(`{"port":[1,2]}`), mkVMess(`{}`),
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("输入 %q 触发 panic: %v", l, r)
				}
			}()
			_, _ = parseVMessLink(l)
		}()
	}
}
