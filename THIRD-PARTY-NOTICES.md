# Third-Party Notices

本项目的二进制发行版包含/链接以下第三方组件，在此致谢并列出其许可信息。
（完整许可证文本随源码仓库对应路径存放，或可在上游仓库获取。）

## 随发行版分发的二进制 / 数据

| 组件 | 来源 | 许可 | 仓库内位置 |
| --- | --- | --- | --- |
| Xray-core | https://github.com/XTLS/Xray-core (v1.8.24) | MPL-2.0 | 以 Go 库链接进主程序 |
| wintun.dll | https://www.wintun.net/ (0.14.1) | 允许随「仅通过其 API 使用」的软件分发 | `cores/wintun.dll`、`cores/wintun-LICENSE.txt` |
| badvpn-tun2socks | https://github.com/ambrop72/badvpn | BSD-2-Clause | `cores/native/bin/badvpn-tun2socks.exe`（可选组件） |
| geoip.dat / geosite.dat | https://github.com/v2fly/geoip 、https://github.com/v2fly/domain-list-community | CC-BY-SA-4.0 / MIT（详见上游） | `geo/`、以 embed 内置 |

## 以 Go 模块链接的主要依赖

- github.com/wailsapp/wails/v2 —— MIT
- gvisor.dev/gvisor —— Apache-2.0
- golang.org/x/sys 等 golang.org/x/* —— BSD-3-Clause
- github.com/xtls/xray-core 及其传递依赖（quic-go、utls、reality、sing 等）—— 各自上游许可（MPL-2.0 / MIT / Apache-2.0 / BSD）
- github.com/energye/systray —— Apache-2.0
- github.com/go-ole/go-ole —— MIT

其余传递依赖以 `go.mod` / `go.sum` 为准，均为各自上游仓库的开源许可。
如需完整的依赖许可清单，可在仓库根目录执行：

```bash
go run github.com/google/go-licenses@latest report ./... 
```
