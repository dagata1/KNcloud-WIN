package main

import (
	"embed"
	"net/http"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// flagTunAutoStart 由 --tun-autostart 参数置位：提权重启链路里，
// 新实例完成初始化后自动进入 TUN 模式（见 elevation_windows.go）。
var flagTunAutoStart bool

// selftestMode 由 --tun-selftest 参数置位：禁止 SimpleConnect 触发提权重启。
var selftestMode bool

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--tun-autostart" {
			flagTunAutoStart = true
		}
		if arg == "--tun-selftest" {
			selftestMode = true
		}
	}

	app := NewApp()

	// 隐藏自检入口：KNcloud-WIN.exe --tun-selftest（需管理员），验证简易模式 SSTap 链路
	if len(os.Args) > 1 && os.Args[1] == "--tun-selftest" {
		os.Exit(runTunSelfTest(app))
	}

	err := wails.Run(&options.App{
		Title:     "KNcloud-WIN",
		Width:     1120,
		Height:    760,
		MinWidth:  380, // 简易模式需要缩到紧凑尺寸（前端切换模式时自动调整）
		MinHeight: 560,
		Frameless: true, // 自定义 Win11 沉浸式标题栏
		AssetServer: &assetserver.Options{
			Assets: assets,
			// 本地资源安全响应头：防 MIME 嗅探、防被嵌入 iframe、防引用泄露。
			// 特意不加 CSP：Wails 运行时 IPC 依赖同源脚本，策略过严会把窗口打成白屏，
			// 收益远小于风险，先以无副作用的三个头为限。
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					h := w.Header()
					h.Set("X-Content-Type-Options", "nosniff")
					h.Set("X-Frame-Options", "DENY")
					h.Set("Referrer-Policy", "no-referrer")
					next.ServeHTTP(w, r)
				})
			},
		},
		BackgroundColour: &options.RGBA{R: 243, G: 243, B: 243, A: 0}, // 透明底色配合 Mica
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		// 托盘常驻后台：用户重复双击图标时唤出已运行实例，
		// 而不是启动第二个进程去抢系统代理与 10808/10809 端口。
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "KNcloud-WIN-win11-single-instance",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				go app.focusFromSecondInstance()
			},
		},
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent:              true,
			WindowIsTranslucent:               true,
			BackdropType:                      windows.Mica, // Windows 11 云母材质！
			Theme:                             windows.SystemDefault,
			DisableFramelessWindowDecorations: false, // 保留 Windows 11 原生圆角与阴影
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
