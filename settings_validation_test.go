package main

import "testing"

func settingsApp() *App {
	a := &App{}
	a.settings = AppSettings{SocksPort: 10808, HttpPort: 10809, Theme: "dark", UiMode: "classic"}
	return a
}

// TestSaveSettingsRejectsDuplicatePorts 两个端口相同必须在保存阶段挡住。
// 顺序执行的端口占用预检发现不了（各自 listen 后立即 close 都会成功），
// 要等内核绑定第二个监听才失败，报错晦涩且非法设置已经落盘了。
func TestSaveSettingsRejectsDuplicatePorts(t *testing.T) {
	a := settingsApp()
	err := a.SaveSettings(AppSettings{SocksPort: 10808, HttpPort: 10808, Theme: "dark", UiMode: "classic"})
	if err == nil {
		t.Fatal("端口相同应当报错")
	}
	if a.settings.HttpPort != 10809 {
		t.Fatalf("报错后不应写入新设置，实际 HttpPort=%d", a.settings.HttpPort)
	}
}

// TestSaveSettingsDuplicateAfterNormalize 非法端口回退为旧值后若与另一个相同，
// 同样要挡住 —— 校验必须发生在归一化之后。
func TestSaveSettingsDuplicateAfterNormalize(t *testing.T) {
	a := settingsApp()
	if err := a.SaveSettings(AppSettings{SocksPort: 10809, HttpPort: 0, Theme: "dark", UiMode: "classic"}); err == nil {
		t.Fatal("归一化后端口相同，应当报错")
	}
}

// TestSaveSettingsNormalizesOutOfRange 越界端口回退旧值，而不是写入垃圾。
func TestSaveSettingsNormalizesOutOfRange(t *testing.T) {
	a := settingsApp()
	if err := a.SaveSettings(AppSettings{SocksPort: 70000, HttpPort: -1, Theme: "dark", UiMode: "classic"}); err != nil {
		t.Fatalf("越界端口应被归一化而非报错: %v", err)
	}
	if a.settings.SocksPort != 10808 || a.settings.HttpPort != 10809 {
		t.Fatalf("越界端口应回退旧值: %+v", a.settings)
	}
}
