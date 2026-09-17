# 第三方组件与许可

KNcloud-WIN 本身以 **MPL-2.0** 发布（见 `LICENSE`）。选择 MPL-2.0 是为了与核心依赖
Xray-core 保持一致 —— 本项目通过源码级 import 嵌入 Xray-core，MPL-2.0 的文件级
copyleft 要求衍生的修改保持同一许可。

下面列出随二进制分发或编译进产物的第三方组件。

## 编译进可执行文件

| 组件 | 许可 | 说明 |
|---|---|---|
| [Xray-core](https://github.com/XTLS/Xray-core) | MPL-2.0 | 代理内核，源码级嵌入（`core.go`） |
| [Wails v2](https://github.com/wailsapp/wails) | MIT | 桌面应用框架 |
| [gVisor](https://github.com/google/gvisor) | Apache-2.0 | `netstack` 用户态 TCP/IP 协议栈，TUN 转发（`tapstack.go`） |
| [energye/systray](https://github.com/energye/systray) | Apache-2.0 | 系统托盘 |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | BSD-3-Clause | Win32 API 绑定 |
| React / Vite / lucide-react | MIT | 前端 |

## 随安装包分发的数据与二进制

| 文件 | 来源 | 许可 |
|---|---|---|
| `cores/wintun.dll` | [Wintun](https://www.wintun.net/) 官方发行包 wintun-0.14.1.zip，未经修改 | 见 `cores/wintun-LICENSE.txt` |
| `geo/geoip.dat`、`geo/geosite.dat` | [v2fly/domain-list-community](https://github.com/v2fly/domain-list-community)、[v2fly/geoip](https://github.com/v2fly/geoip) | MIT |
| `geo/geosite-cn.srs`、`geo/cn-routes.txt` | 同上，转换后的分流数据 | MIT |

### 关于 wintun.dll

Wintun 的预编译二进制许可**不是**通用开源许可，使用时请注意其中两条约束：

1. 只允许通过其公开 API 调用，不得反编译或修改 DLL 本体 ——
   本项目仅通过 `LoadLibrary` + 导出函数调用（`tapstack.go` 的 `loadWintunAPI`），符合该约束；
2. 必须随附原始许可文本 —— 即仓库中的 `cores/wintun-LICENSE.txt`，请勿删除。

### 关于已移除的 sing-box

早期版本曾内嵌 `cores/sing-box.exe`（GPLv3）作为 TUN 实现。该方案已被
`tapstack.go`（wintun + gVisor，进程内转发）完全取代，相应二进制与代码已从仓库移除，
因此本项目当前**不包含任何 GPL 组件**。
