# SSTap × v2rayN 融合架构

> **文档约定**：本文只描述**仓库中已存在的代码**。凡属规划、设想或待评估的内容，
> 一律收进文末「[尚未实现](#尚未实现)」与「[明确不做](#明确不做)」两节，并标注原因。
> 文中每项实现均可通过给出的文件与行号核对。
>
> 对应代码版本：移除 sing-box 链路之后（`tun.go` 已由 1255 行精简至 541 行）。

---

## 1. 为什么要做这个融合

两类工具各自解决了一半问题：

| | v2rayN 一类 | SSTap 一类 |
|---|---|---|
| **接管层次** | 应用层（本地 SOCKS/HTTP 端口 + Windows 系统代理） | 三层（虚拟网卡直接收发 IP 报文） |
| **协议能力** | VLESS / Reality / VMess / Trojan 等现代抗封锁协议 | 仅 SS / SOCKS5 等老协议 |
| **典型短板** | 不读系统代理的进程会直连泄漏：Unity / Unreal 游戏、命令行工具、后台服务、各类不提供代理设置的桌面程序 | 2017 年后停止维护，无现代协议、无订阅生态 |

本项目取两者的长处：**用 SSTap 的 L3 接管方式抓全量流量，用 Xray-core 处理现代协议出站。**

**注意**：L3 接管解决的是「流量能否被捕获」，不是「延迟能否降低」。它对游戏的价值在于
让原本绕过代理直连的流量也能走节点，而非加速本身。

---

## 2. 整体数据通路

```
┌─────────────────────────────────────────────────────────────────────┐
│ 用户应用                                                             │
│ 浏览器 / Steam / 网游客户端 / CMD / 后台服务 / 任何不读系统代理的进程 │
└─────────────────────────────────────────────────────────────────────┘
                   │ 原生 IP 报文（TCP / UDP）
                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│ L3 接管层        tapstack.go (1061 行) + tun.go (541 行)             │
│                                                                     │
│  虚拟网卡    wintun.dll，固定 GUID，常驻复用，从不销毁                │
│  协议栈      gVisor netstack（用户态 TCP/IP，纯 Go）                 │
│  路由分流    Windows MIB 路由表 API，策略由 sstap.go 计算            │
│  防回环      节点 IP 写 /32 直连路由，指向物理网卡真实网关            │
│  防泄漏      网卡 metric 提至最高；2000::/3 路由堵 IPv6              │
└─────────────────────────────────────────────────────────────────────┘
                   │ 转成 SOCKS5（TCP: CONNECT，UDP: UDP ASSOCIATE）
                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│ 出站协议层       core.go (495 行)                                    │
│                                                                     │
│  Xray-core 源码级嵌入，常驻运行，监听本地 SOCKS 入站                  │
│  VLESS(+Reality/Vision) / VMess / Trojan / Shadowsocks               │
│  传输层：tcp / ws / grpc / httpupgrade，安全层：tls / reality         │
└─────────────────────────────────────────────────────────────────────┘
                   │ 加密后经物理网卡出站
                   ▼
              节点服务器
```

**分工**：Xray 常驻充当「代理大脑」，TUN 层只负责「抓流量并转成 SOCKS5」。
两层通过本地 SOCKS 端口解耦 —— 这是能同时保留两边生态的关键。

---

## 3. 已实现的关键机制

### 3.1 常驻虚拟网卡（对应 SSTap 的固定 GUID 网卡）

SSTap 快的根本原因不是协议栈，而是**网卡不重建**：TAP-Windows 适配器装一次永久存在，
开关全局代理只是增删路由表条目。

被移除的 sing-box 方案每次开关都要「杀进程 → pnputil 删残留设备 → 等待网卡消失 → 重建」，
慢在网卡生命周期管理，且残留设备会越积越多。

当前实现（`tapstack.go`）：

- 以固定 GUID `{6e4a2c31-8d5f-4b9e-...}` 定位适配器，**存在即复用，不存在才创建**
  （`knTapGUID`，`tapstack.go:52`）
- `ensure()` 先 `wintunOpenAdapterByName` 尝试打开，失败才创建（`ensure()`，`tapstack.go:241`）
- `closeSession()` 只结束读写会话，**不删除适配器** —— 注释明确标注（`closeSession()`，`tapstack.go:279`）
- 网卡名 `KNcloud-TAP`，MTU 1500（与 TAP-Windows 默认一致，兼容性最好）

因此开关 TUN 只是路由表操作，无需等待驱动层的设备创建/销毁。

### 3.2 用户态协议栈与转发出口

`startTapForwarding`（`tapstack.go:624`）构建 gVisor 协议栈：

```go
NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol}
TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}
```

三类流量分别处理：

| 流量 | 出口 | 代码位置 |
|---|---|---|
| TCP | SOCKS5 CONNECT → 本地 Xray 入站 | `tcp.NewForwarder`，`tapstack.go:667` |
| UDP（非 53） | SOCKS5 UDP ASSOCIATE → 本地 Xray 入站 | `relayUDPSocks`，`tapstack.go:859` |
| UDP 53（DNS） | **经物理网卡直连公共 DNS**，不走代理 | `relayDNS`，`tapstack.go:810` |

**UDP 是完整支持的**（`socksDialUDP` 实现了完整的 UDP ASSOCIATE 握手，
`tapstack.go:514`）—— 这是网游流量能被接管的前提，也是与纯 HTTP 代理方案的本质差别。

### 3.3 DNS 处理（与常见描述不同，需注意）

实际行为分两步，容易被误述为「fake-ip 劫持」：

1. 虚拟网卡的系统 DNS 被设为 `198.18.0.2`（`tunDnsAddr`，属 `198.18.0.0/15` 基准测试段），
   使整机 DNS 查询进入 TUN；
2. gVisor 捕获到目标端口 53 的 UDP 后，交由 `relayDNS` **经物理网卡直发公共 DNS**
   （`223.5.5.5` / `119.29.29.29`），应答再伪装成原始目标地址回给客户端。

即 `198.18.0.2` 只是**把查询引进 TUN 的诱饵地址**，本身不运行 DNS 服务，
也没有 fake-ip 应答生成。这样做同时避免了 DNS 污染与查询回环。

> 该处常量注释原写作「端口 53 被 sing-box 劫持」，属 sing-box 移除后的遗留描述，
> 已随本文档一并修正（`tun.go:36`）。

### 3.4 防回环：节点 IP 的 /32 直连路由

TUN 生效后若不加处理，Xray 自己发往节点服务器的加密流量会被自己的虚拟网卡吸走，
形成死循环。`applySstapRouting`（`tun.go:399`）的处理：

1. `lookupNodeIPv4s` 在 DNS 劫持生效前解析节点地址，并剔除污染应答（组播/保留段）；
2. `getBestRoute` 取该 IP 在**物理网卡**上的真实最优路由（含网关、接口索引）；
3. 写入 `/32` 主机路由，`Metric1 = interfaceMetric4(物理网卡) + 1`，确保优先级最高
   （`tun.go:443`）；
4. 记入 `tunHostRoutes`，断开时由 `removeHostRoutes` 精确回收。

支持 on-link 网关与 PPPoE（按 `NextHop == 0` 判定 Direct/Indirect 类型），
并对「路由已存在」（错误码 5010）做幂等处理 —— 上次异常退出的残留会被纳管而非报错。

### 3.5 分流策略

`sstap.go` 的 `sstapPolicyRoutes`（`sstap.go:140`，纯函数，可测试）计算路由集合：

| 策略 | 行为 |
|---|---|
| `bypass-cn`（默认） | 大陆 IP + 保留网段直连，其余进 TUN（取补集） |
| `global` | `0.0.0.0/1` + `128.0.0.0/1` 全量进 TUN |
| `proxy-cn` | 仅大陆 IP 进 TUN |
| `sstap:<file>` | **直接解析 SSTap 原版 `.rules` 文件**，支持 Skip 取补集 |

对 SSTap `.rules` 的原生兼容意味着老用户的规则文件可直接迁移。

### 3.6 IPv6 泄漏处理

向 TUN 注入 `2000::/3`（全部全球单播 IPv6）路由（`addTunIPv6Route`，`tun.go:516`），
使 IPv6 流量无法绕过 TUN 直出。停止时由 `removeTapRouting` 的 PowerShell 命令一并移除
（`tapstack.go:963`）。

### 3.7 两种交互形态

- **简易模式**（`uiMode='simple'`，登录后默认）：一键连接 + 策略切换，接近 SSTap 的加速器形态
- **经典模式**：节点多选（Ctrl+A / Ctrl+点击 / Shift+范围）、批量真连接测速、批量删除、订阅管理

---

## 4. 尚未实现

以下内容**当前代码中不存在**，列出以免与已实现部分混淆。

### 4.1 Hysteria2 —— 可导入，但无法连接 ⚠️

这是目前最需要注意的落差：

- `sharelink.go:28` **能解析** `hysteria2://` 与 `hy2://`，节点可导入、可在列表显示；
- 但 `core.go` 的 `buildProxyOutbound` 只有 VLESS / VMess / Trojan / Shadowsocks 四个分支，
  Hysteria2 落入 `default`，直接返回错误：

```go
return nil, fmt.Errorf("Xray core does not support %s", node.Protocol)
```

**根因是架构性的**：Hysteria2 基于 QUIC，Xray-core 不提供该出站。补一个 case 解决不了，
必须引入第二个内核（如 sing-box）。而 sing-box 刚刚被移除，且它是 GPLv3 —— 重新引入
会让本项目重新受 GPL 约束（详见 `THIRD-PARTY-NOTICES.md`）。

当前状态（**导入成功但一连接就报错**）是最差的用户体验，无论最终走哪条路，
都应先在 UI 上明确标注该协议不可用。

三种可选方向，尚未决策：

| 方案 | 代价 |
|---|---|
| A. 放弃 Hysteria2，明确只做 Xray 系协议 | 单内核 / 无 GPL / 体积不变；需在 UI 标灰并说明 |
| B. 引入 sing-box 作第二内核 | 重回 GPLv3、体积 +30MB、双内核生命周期管理复杂度 |
| C. 暂缓，先打磨 L3 接管这一核心优势 | HY2 维持不可用，但**必须先修 UI 提示** |

### 4.2 热待机无缝换节点

网卡常驻是**已实现**的（见 3.1），因此切换时不必等待网卡重建 —— 这部分成立。

但切换节点仍会重启 Xray 内核，**既有连接会断开**。原 sing-box 链路中的
`tunWarm` / `tunWarmNode` 热待机字段已随该链路一并删除。

要做到真正无感，需实现 Xray 出站的热重载（保留入站监听与既有连接、仅替换出站），
属于独立课题。

### 4.3 其它

- **流式延迟推送**：`PingNodes` 目前是批量并发测速后统一返回，非逐节点流式推送
- **TCP 快速回收 / 连接级统计**：当前统计基于内核 stats 计数器，无单连接粒度

---

## 5. 明确不做

### 5.1 双模协议栈（badvpn-tun2socks + TAP-Windows 降级）

原设计设想「优先调用 C/lwIP 编写的 badvpn-tun2socks 挂载 SSTAP 驱动，未安装时降级到 Go 栈」。

**不采纳**，理由：

1. **代码中零实现** —— `badvpn` / `tun2socks` / `lwip` / `tap-windows` 全仓库无任何引用，
   仅在 `tapstack.go` 注释中作为设计参照被提及；
2. **方向性倒退** —— Wintun 是 WireGuard 作者为替代 TAP-Windows 而写的现代驱动，
   开销更低、无需用户手动安装老驱动。回退到 TAP-Windows 意味着重新引入驱动安装步骤；
3. **引入 C 依赖** —— 破坏当前纯 Go、交叉编译即可出包的构建模型（现在
   `GOOS=windows go build` 一条命令即可产出完整 exe）；
4. **双栈维护成本** —— 两套协议栈的行为差异会让 bug 复现与定位成本翻倍。

**结论**：单一 Wintun + gVisor 路径。若未来出现 Wintun 不可用的真实场景，
应优先改进错误提示与安装引导，而非引入第二套栈。

---

## 6. 与原始 SSTap 的对应关系

| SSTap 组件 | 本项目对应 | 位置 |
|---|---|---|
| TAP-Windows 网卡（固定 GUID） | wintun.dll 常驻适配器（固定 GUID） | `tapstack.go:52` |
| ss-tap / tun2socks | gVisor netstack 用户态栈 | `tapstack.go:624` |
| ss-local 隧道 | Xray-core 出站（现代协议） | `core.go` |
| unbound DNS | gVisor 拦截 53 → 物理网卡直连公共 DNS | `tapstack.go:810` |
| `.rules` 规则文件 | 原生解析，直接兼容 | `sstap.go` |
| 路由表分流 | Windows MIB API | `tun.go:399` |

---

## 7. 核心文件

| 文件 | 行数 | 职责 |
|---|---|---|
| `tapstack.go` | 1061 | wintun 适配器 + gVisor 栈 + TCP/UDP/DNS 转发（TUN 唯一实现） |
| `app.go` | 1124 | `App` 结构体、Wails 绑定方法、节点/订阅/设置/生命周期 |
| `tun.go` | 541 | Windows 路由表操作、`applySstapRouting`、IPv6 路由 |
| `core.go` | 495 | Xray-core 嵌入、配置生成、geo 资源、真连接测速 |
| `sstap.go` | 162 | 分流策略引擎、SSTap `.rules` 解析（纯函数） |

> `tun.go` 中的 sing-box 子进程链路（启停、Job 对象、pnputil 设备清理、
> 配置生成，共 715 行）已整体删除，其职责由 `tapstack.go` 承担。
