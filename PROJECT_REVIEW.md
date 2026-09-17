# KNcloud-WIN 项目评估报告

评估日期：2026-09-17
评估对象：`dagata1/KNcloud-WIN` @ `1297132`

> **状态更新**：本报告中标记为 P0 / P1 的问题，以及大部分 P2 项，已在后续提交中修复。
> 每项的落地情况见文末「修复记录」。
代码规模：Go 约 6,700 行（其中测试 ~690 行）+ React 前端约 2,980 行

---

## 一、总体结论

这是一个**完成度相当高、工程细节扎实**的 Windows 代理客户端。它不是常见的"UI 壳子 + 调用外部内核"的玩具项目：Xray-core 被嵌入编译进二进制，TUN 层是基于 wintun + gvisor 自研的转发栈，路由分流复刻了 SSTap 的静态路由方案，这些都是有真实技术含量的部分。

但项目当前处于**"功能冲刺完成、尚未做收敛整理"**的状态，存在 1 个会导致每次退出都卡死的阻断性缺陷，以及约 600 行已经不可达的历史架构残留。

**评级：**

| 维度 | 评分 | 说明 |
|---|---|---|
| 功能完整度 | ★★★★☆ | 订阅/节点/分流/TUN/托盘/账户体系齐全 |
| 代码正确性 | ★★☆☆☆ | 存在退出死锁 + 数据竞争 |
| 架构清晰度 | ★★☆☆☆ | 两代 TUN 架构并存，旧代码未删 |
| 注释与可读性 | ★★★★★ | **项目最大亮点**，见下文 |
| 工程化程度 | ★★☆☆☆ | CI 不跑测试，无 LICENSE，单次提交历史 |
| 安全性 | ★★★☆☆ | 凭证明文落盘，AllowLan 无认证 |

---

## 二、突出优点

### 1. 注释质量远超一般开源项目

这是通读代码后最直观的感受。注释不是复述代码在做什么，而是记录**为什么这么做**，且几乎每条都包含踩坑的具体现象。举几个例子：

`tun.go:372` 解释为什么写路由必须叠加接口 metric：

> Vista 之后旧版路由 API 的 `MIB_IPFORWARDROW.Metric1` 语义变为「接口 metric + 路由 metric」的合成值，`CreateIpForwardEntry` 传入小于接口 metric 的值会被 `ERROR_INVALID_PARAMETER` 拒绝。

`tools/syncbuild/main.go` 解释为什么要专门写个 Go 程序来复制文件：

> xcopy：目标文件不存在时会交互式追问「是文件名还是目录名」，会把构建永久挂住；robocopy：成功时退出码是 1，wails 会把它当成构建失败。

`tun.go:284` 解释为什么不用 `x/sys` 的新 API：

> x/sys 的 `MIB_IPFORWARD_ROW2` 布局与系统实测不符——实测每字段偏移 +4，读写皆错。

这类注释对后续维护者（以及半年后的作者本人）价值极高，属于应当明确保留和继续坚持的工程习惯。

### 2. Windows 平台细节处理到位

- **单实例锁**：避免第二个进程抢占系统代理与 10808/10809 端口
- **Job Object (`KILL_ON_JOB_CLOSE`)**：主进程崩溃时系统自动回收子进程
- **系统代理还原**：退出时清理，不留死代理（这是同类软件最常见的用户投诉点）
- **配置原子写**：`savePersisted` 走 `.tmp` + `rename`，避免断电写坏配置
- **DNS 污染兜底**：`lookupNodeIPv4s` 发现系统 DNS 返回的全是保留段地址时，改用公共 DNS 直查
- **防回环路由**：为节点服务器 IP 写 /32 直连路由，避免代理流量被自己的分流路由吸回 TUN

### 3. 有针对性的回归测试

`log_race_test.go` 不是凑数的测试。它针对一个真实修复过的并发 bug 设计负载，并且注释里说明了为什么不用 `-race`（本机 windows/386 无 C 编译器），以及如何验证负载强度够（"用同一套负载跑修复前的无锁实现，3 次里 2 次出现丢写入 + 重复 ID"）。这是很成熟的测试思路。

`tun_test.go` 中的 `TestBuildTunConfigValid` 会调用真实的 `sing-box check` 校验生成的配置，也是有效的集成测试设计。

---

## 三、必须修复的问题

### 🔴 P0 — 每次退出都会死锁（进程永久挂起）

`sync.RWMutex` 在 Go 中**不可重入**。`cleanup()` 持有写锁的情况下调用了同样要抢写锁的 `stopWebLogin()`：

```go
// app.go:1009
func (a *App) cleanup() {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.systemProxy { setWindowsSystemProxy(false, ""); a.systemProxy = false }
    a.stopWebLogin()      // ← 这里
    a.stopCoreLocked()
    a.tunSoftStopLocked()
    a.savePersisted()
}

// weblogin.go:157
func (a *App) stopWebLogin() {
    a.mu.Lock()           // ← 永久阻塞
    m := a.webLogin
    a.mu.Unlock()
    ...
}
```

**触发路径**：托盘「退出」→ `quitApp()` → `runtime.Quit()` → `beforeClose()` → `cleanup()` → 卡死。
关闭窗口且未开启"最小化到托盘"时同样走这条路径。

**实际后果**（按代码顺序）：
1. 系统代理已还原 ✅（在死锁之前执行）
2. `stopCoreLocked()` 未执行 → Xray 内核不停，端口不释放
3. `tunSoftStopLocked()` 未执行 → **TUN 路由表不回滚，用户网络可能持续异常**
4. `savePersisted()` 未执行 → **本次会话的节点/设置/流量统计全部丢失**
5. 进程永久挂起，只能用任务管理器强杀

这个 bug 的隐蔽之处在于：系统代理还原恰好在死锁点之前，所以用户上网看起来是正常的，只会觉得"程序退不掉"。

**修复方案**（两种任选）：

```go
// 方案 A：拆出不加锁的内部版本（推荐，与项目现有 *Locked 命名约定一致）
func (a *App) stopWebLoginLocked() {
    m := a.webLogin
    if m == nil { return }
    m.mu.Lock()
    defer m.mu.Unlock()
    m.stopLocked()
}

func (a *App) stopWebLogin() {
    a.mu.Lock()
    m := a.webLogin
    a.mu.Unlock()
    if m == nil { return }
    m.mu.Lock(); defer m.mu.Unlock()
    m.stopLocked()
}
// cleanup() 中改调 a.stopWebLoginLocked()
```

```go
// 方案 B：在 cleanup 进入临界区之前调用
func (a *App) cleanup() {
    a.stopWebLogin()   // 移到锁外
    a.mu.Lock()
    defer a.mu.Unlock()
    ...
}
```

> 建议顺手补一条回归测试（参照 `log_race_test.go` 的风格），用 `time.After` 断言 `cleanup()` 能在超时内返回。

---

### 🟠 P1 — `a.ctx` 数据竞争

`a.ctx` 在 `startup()` 中持锁写入，但有三处**不持锁读取**：

| 位置 | 代码 |
|---|---|
| `app.go:243` | `case <-a.ctx.Done():`（流量采样 goroutine） |
| `app.go:566` | `if a.ctx != nil { runtime.EventsEmit(a.ctx, ...) }`（`PingNode`） |
| `app.go:1032` | `ctx := a.ctx`（`quitApp`） |

项目里已经有了正确的封装 `appCtx()`（`app.go:1052`，持 RLock 读取），只是这三处没用上。

`PingNode` 的这一处风险最实在：`PingNodes` 会并发拉起最多 3 个 goroutine 同时调用 `PingNode`，与启动阶段的 `a.ctx` 写入存在真实的竞争窗口。

**修复**：三处统一改用 `a.appCtx()`。`app.go:243` 需要在 goroutine 启动前把 ctx 捕获为局部变量。

---

### 🟠 P1 — 约 600 行不可达代码，含整条已废弃的 sing-box 架构

项目经历过一次 TUN 架构重写：**旧方案**（`tun.go`，拉起 `sing-box.exe` 子进程）已被**新方案**（`tapstack.go`，Go 内进程 wintun + gvisor 协议栈）完全取代，但旧代码一行没删。

从 `main` 做可达性分析的结果：

| 文件 | 不可达函数 | 约计行数 |
|---|---|---|
| `tun.go` | `startTunLocked`、`stopTunLocked`、`ensureSingBoxBin`、`ensureWintunDLL`、`killOrphanSingBox`、`warmStopTunLocked`、`reapplyTunRoutesLocked`、`waitForTunIface`、`createKillOnCloseJob`、`removeResidualWintunDevices` 等 15 个 | ~421 |
| `tapstack.go` | `tunLinkEndpoint` 的 14 个方法（gvisor 接口实现，由接口动态分发，**属于误报**） | ~156 |
| `autostart_windows.go` | `isAutoStartEnabled` | ~15 |

关键证据：`startTunLocked` 在整个代码库中只有定义处一处引用，无任何调用方。整个 sing-box 子进程路径是死的。

**连带影响：`cores/sing-box.exe` 是 30MB，占仓库 73 个文件总体积的一半**（仓库 pack 约 19.85 MiB，工作区 `cores/` 32MB）。这个二进制唯一的引用来自死代码 `ensureSingBoxBin`。

> 注意：`cores/wintun.dll` **不能删** —— 新架构的 `tapstack.go:90` 仍在用 `wintunDLL` 这个 embed 变量。
> 同理 `geo/` 下 26MB 的 geoip/geosite 是真实在用的（`core.go:47`），也要保留。
> `App` 结构体里的 `tunCmd` / `tunJob` / `tunProcDone` / `tunWarm` / `tunWarmNode` 五个字段同样只服务于死掉的旧路径，可一并清理（`tun_selftest.go:122` 有引用，需同步调整断言）。

**建议**：单独开一个 `refactor: remove legacy sing-box TUN path` 提交，删除死函数、`//go:embed cores/sing-box.exe`、`cores/sing-box.exe` 本体及相关结构体字段。这一步能让 `tun.go` 从 1,255 行缩到 800 行出头，并显著减小仓库和构建产物体积。

---

## 四、应当关注的问题

### 🟡 设置页有三个"假开关"

**「底层 Core 类型」下拉框完全无效。** 前端提供 `Xray-core` / `Sing-box` / `V2Ray-core` 三个选项，但后端 `CoreType` 字段的全部用途只是在 `GetCoreStatus()` 里原样回显（`app.go:649`），从不参与内核选择——`startCoreLocked()` 永远硬编码启动 Xray。用户选了 "sing-box (高性能现代核心)" 之后什么都不会发生。

建议要么移除该下拉框，要么标注为"暂不可用"。

同类问题：
- **批量删除未接线**。提交信息宣称支持 "batch delete"，后端 `DeleteNodes([]string)` 也确实实现了，但前端只调用单条的 `DeleteNode`，`DeleteNodes` 在 `App.jsx` 中出现 0 次。批量测速（`PingNodes`）是接好了的。
- **4 个已绑定方法前端从未调用**：`ToggleSystemProxy`、`SkipLogin`、`CancelWebLogin`、`IsWindowMaximized`。`CancelWebLogin` 缺失意味着用户点了网页登录后若想中断，只能干等 5 分钟超时。

### 🟡 启动即自动接管系统代理，无用户确认

`startup()` 无条件拉起内核并写入系统代理注册表（`app.go:287-300`），不看任何"是否自动连接"的设置项。叠加默认开启的开机自启（`AutoStart: true`）和默认开启的最小化到托盘，效果是：**用户开机即被静默接管系统代理，且窗口不会出现**。

对代理软件来说这个默认行为偏激进，建议增加一个 `autoConnect` 设置项，默认关闭或至少可关闭。

### 🟡 凭证明文存储且暴露给前端

V2Board 的 `auth_data` token 以明文写入 `%APPDATA%\KNcloud\config.json`（`account.go:26`，权限 0644），同时通过 `AccountInfo` 结构体原样暴露到前端（`models.ts:13` 有 `authToken: string`）。

前端实际并不需要这个 token。建议：
1. 给 `AuthToken` 字段加 `json:"-"`，改用单独的内部结构体持久化；
2. 落盘前用 DPAPI（`CryptProtectData`）加密——项目已经在大量调用 Win32 API，接入成本很低。

### 🟡 `AllowLan` 开启后是无认证的开放代理

`core.go` 中 SOCKS5 入站固定 `"auth": "noauth"`，`AllowLan` 打开时监听地址变为 `0.0.0.0`。这意味着同一局域网（含公共 WiFi）内任何设备都能无凭证使用该代理。

这是 v2rayN 等软件的通行做法，不算缺陷，但建议在设置页该开关下方补一句风险提示。

### 🟡 CI 只构建，不跑测试

`.github/workflows/build.yml` 有完整的 Wails 构建 + Release 流程，但**没有 `go test` 步骤**，也没有 `go vet`。

项目已经写了 14 个测试用例，却没有任何一次自动执行。而 P0 死锁这类问题，恰恰是 `go vet` 之外、但一条简单的超时测试就能拦住的。

建议在 `Build` 之前插入：

```yaml
      - name: Vet & Test
        shell: bash
        run: |
          go vet ./...
          go test ./... -short -timeout 5m
```

注意：`clipboard_test.go` 会改写系统剪贴板、`tun_test.go` 部分用例依赖真实路由表和 `sing-box` 二进制，需要用 `testing.Short()` 做跳过保护后才适合进 CI。

---

## 五、工程化与合规

| 项目 | 现状 | 建议 |
|---|---|---|
| **LICENSE** | **完全缺失** | 必须补。项目嵌入 Xray-core（MPL-2.0）、gvisor（Apache-2.0）、wintun（有专门的分发条款）、sing-box（GPLv3），其中 GPLv3 的传染性需要认真评估——**这恰好是删除 sing-box.exe 的另一个理由** |
| **模块名** | `module v2rayN-win11` | 与产品名 KNcloud-WIN 不符，属早期脚手架残留，建议改名 |
| **go.mod** | 末行有注释掉的本地 `replace` 指向 `C:\Users\Admin\go\pkg\mod` | 清理掉 |
| **提交历史** | 只有 1 个 commit，包含 73 个文件全量 | 后续按功能拆分提交，便于 review 和二分定位 |
| **README** | 未提及 TUN 模式、系统托盘、账户登录、网页授权等主要功能；仍写着"Hysteria2 不支持" | 与当前功能对齐 |
| **.gitignore** | 含 `wintun.dll` 规则，但 `cores/wintun.dll` 已被跟踪 | 改为更精确的路径规则，避免误导 |
| **平台约束** | `app.go` / `tun.go` / `tapstack.go` / `tun_selftest.go` 都 import 了 `golang.org/x/sys/windows` 却没有 `_windows` 后缀或构建标签 | 非 Windows 平台上 `go vet ./...` 会直接失败；加 `//go:build windows` |

---

## 六、前端评价

**整体质量不错**：Win11 Fluent/Mica 视觉还原度高，Ctrl+A / Ctrl+点击 / Shift+范围选择的多选交互完整，切换节点做了乐观更新（先改 UI 再等后端），加载态和 toast 提示都覆盖到位。构建产物 249KB / gzip 76KB，对于这个功能量是合理的。

主要问题是 **`App.jsx` 单文件 1,830 行、单个组件内 37 处 `useState`/`useEffect`**。所有页面（仪表盘 / 服务器 / 订阅 / 路由 / 日志 / 设置 / 登录）和所有弹窗都挤在一个组件里。

建议的拆分方向（不必一次到位）：
1. 先抽 `useCoreStatus()` / `useNodes()` / `useAccount()` 三个自定义 hook，把轮询和状态同步逻辑收进去；
2. 再把 6 个 Tab 页面拆成独立组件文件；
3. 弹窗（添加节点 / 导入链接）抽成受控组件。

另外前端状态靠定时轮询 `GetCoreStatus` + `GetLogs` 维持，而后端已经在用 Wails 事件（`kncloud:node-delay`、`kncloud:web-login`）了——状态推送也可以逐步改成事件驱动，降低空转开销。

---

## 七、修复优先级建议

| 优先级 | 事项 | 预估工作量 |
|---|---|---|
| 1 | 修复 `cleanup()` 退出死锁 + 补回归测试 | 30 分钟 |
| 2 | 三处 `a.ctx` 改用 `appCtx()` | 15 分钟 |
| 3 | CI 增加 `go vet` + `go test`；给 Windows 专属文件加构建标签 | 1 小时 |
| 4 | 补 LICENSE，评估 sing-box GPLv3 影响 | 1 小时 |
| 5 | 删除 sing-box 死代码与 30MB 二进制 | 2 小时 |
| 6 | 移除/禁用"假的"Core 类型下拉框，接线批量删除 | 1 小时 |
| 7 | `authToken` 不下发前端 + DPAPI 加密落盘 | 半天 |
| 8 | 增加 `autoConnect` 设置项 | 半天 |
| 9 | `App.jsx` 拆分重构 | 1-2 天 |

---

## 八、一句话总结

**底子很好，注释水平是加分项，但有一个"每次退出都卡死并丢配置"的 P0 缺陷必须马上修；修完之后最值得做的是把上一代 sing-box 架构的 600 行死代码和 30MB 二进制清理掉，让架构重新变得只有一条路径。**

---

# 修复记录

以下改动已实施并通过 Windows 交叉编译（Go 1.25.1）、`go vet` 与 `-race` 测试验证。

## 已修复

| # | 问题 | 处理方式 |
|---|---|---|
| 1 | **P0 退出死锁** | 新增 `stopWebLoginLocked()`（假定调用方持锁的变体），`cleanup()` 改调它。附回归测试 `TestCleanupDoesNotDeadlock`，用超时断言捕获自锁 |
| 2 | **P1 `a.ctx` 数据竞争** | `PingNode` 改用既有的 `appCtx()`；流量采样 goroutine 改为在启动前捕获 `ctx` 局部变量。`quitApp` 原本就在锁内读取，无需改动 |
| 3 | **P1 死代码** | 删除整条 sing-box 子进程链路（`startTunLocked` / `stopTunLocked` / `warmStopTunLocked` / `reapplyTunRoutesLocked` / `ensureSingBoxBin` / `createKillOnCloseJob` / pnputil 设备清理等），及其 `App` 结构体字段（`tunCmd` / `tunJob` / `tunProcDone` / `tunWarm` / `tunWarmNode` / `tunReplacedCore`）。`tun.go` 从 1255 行降到 540 行 |
| 4 | **30MB 二进制** | 移除 `cores/sing-box.exe` 与其 `//go:embed`。**同时消除了项目唯一的 GPLv3 依赖** |
| 5 | **LICENSE 缺失** | 补 MPL-2.0（与核心依赖 Xray-core 一致）+ `THIRD-PARTY-NOTICES.md`，含 wintun 预编译二进制的特殊条款说明 |
| 6 | **CI 不跑测试** | workflow 在构建前增加 `go vet -unsafeptr=false ./...` 与 `go test -short`。会动剪贴板 / 拉起浏览器的用例加 `testing.Short()` 保护 |
| 7 | **「假开关」Core 类型** | 三选一下拉框改为只读展示（后端永远启动 Xray，选项无任何效果） |
| 8 | **批量删除未接线** | 前端接上后端既有的 `DeleteNodes`，多选工具栏新增「删除选中」按钮（带二次确认） |
| 9 | **网页登录无法取消** | 等待授权时显示「取消网页登录」按钮，调用已绑定但从未使用的 `CancelWebLogin`，不必干等 5 分钟超时 |
| 10 | **启动即静默接管系统代理** | 新增 `autoConnect` 设置项（默认关闭）+ 首选项开关。**升级用户保持原行为**：旧配置缺该字段时回填 `true` |
| 11 | **凭证下发前端** | `AccountInfo.AuthToken` 改为 `json:"-"`，不再进 WebView；持久化改走 `persistedConfig.AccountToken`，并保留读取旧配置的兼容路径（`legacyAccountToken`），升级不掉登录态 |
| 12 | 模块名 / 残留配置 | `module v2rayN-win11` → `github.com/dagata1/KNcloud-WIN`；删掉指向 `C:\Users\Admin` 的注释 replace |
| 13 | `.gitignore` 误伤 | 裸 `wintun.dll` 规则会连带忽略需入库的 `cores/wintun.dll`，改为 `/wintun.dll` 锚定根目录 |
| 14 | README 失真 | 补齐 TUN / 托盘 / 账户登录 / 多选等功能，新增测试与许可章节，补上 AllowLan 无认证的风险提示 |
| 15 | 代码格式 | `gofmt -w` 全量（6 个文件，纯空白与 import 排序） |

## 更正报告中的一处判断

初版报告称删除 `cores/sing-box.exe` 能「显著减小构建产物体积」。**这是错的**，实测后更正：

Go 链接器会对「仅被不可达代码引用的 embed 变量」做死代码消除。实测对照（32MB embed）：

| 场景 | 产物体积 |
|---|---|
| embed 变量被 `main` 实际使用 | 34.5 MB |
| embed 变量仅被永不调用的函数引用 | **2.26 MB** |

即那 30MB 本就没有进入发布的 exe（删除前后均为 72.4MB）。真正的收益是**仓库与克隆体积**（pack 从 19.85 MiB 降至约 4 MiB）以及**许可合规**（去掉 GPLv3），而非二进制大小。

## 未处理（建议后续单独进行）

| 项 | 原因 |
|---|---|
| `App.jsx` 拆分（1800+ 行、37 处 hook） | 纯结构性重构，改动面大且无法在无 Windows 环境下做 UI 回归，不宜与本轮缺陷修复混在一起 |
| token 落盘改用 DPAPI 加密 | 已先行阻断「下发前端」这一更大的暴露面；DPAPI 需真机验证加解密与迁移，留作独立改动 |
| 4 处 `unsafe.Pointer` vet 告警 | Win32 互操作的固有写法（`unsafe.Slice` + `LazyProc.Call`），无法改写成 vet 认可的形式，故在 CI 中以 `-unsafeptr=false` 精确豁免 |
| 轮询改事件驱动 | 属优化而非缺陷，后端事件机制已具备，可渐进迁移 |

## 验证方式

沙箱内无 Windows，故采用以下手段验证：

- **交叉编译**：`GOOS=windows GOARCH=amd64 go build` 全量通过，产出 72MB exe
- **静态检查**：`go vet -unsafeptr=false ./...` 退出码 0
- **测试编译**：`go test -c` 通过（Windows 测试二进制可正常产出）
- **运行时验证**：把 `cleanup()` / `stopWebLogin*` / `legacyAccountToken` 等**原样抽取**到可移植工程中，在 Linux 上以 `-race` 实际执行 —— 8 个用例全部通过；其中死锁用例在修复前必定超时失败（实测卡在 `restoreSystemProxy` 之后，印证了「配置丢失 + TUN 路由残留」的推断）
