# KNcloud-WIN

基于 Wails + React + Xray-core 的 Windows 11 原生代理客户端（Fluent / Mica 视觉风格）。

## 功能

- **真实代理内核**：内置 Xray-core（嵌入编译，无需外部内核文件），支持 VLESS / VMess / Trojan / Shadowsocks 节点，本地提供 SOCKS5 与 HTTP 代理入站
- **TUN 全局模式（SSTap 方案）**：虚拟网卡接管全部流量（含游戏等不支持代理的程序）。应用以普通权限启动，仅在开启 TUN 时请求一次管理员授权（自动提权重启）；开启后做端到端自检，链路不通会自动回滚并提示。分流策略与路由模式一致，DNS 经物理网卡直连公共 DNS 防污染
- **订阅管理**：真实拉取订阅链接（自动识别 Base64 / 明文分享链接列表），解析 vmess:// vless:// trojan:// ss:// hysteria2:// 链接，自动过滤机场"流量信息/套餐到期"伪节点；支持订阅更新与删除
- **分享链接导入**：服务器页「导入分享链接」弹窗，支持每行一条批量导入或直接粘贴 Base64 订阅内容
- **节点测速**：真实 TCP 拨测延迟（单节点 / 全部）
- **路由分流**：绕过大陆（geoip:cn / geosite:cn 直连，内置 geoip.dat / geosite.dat，首次运行自动释放到配置目录并做完整性校验）、全局代理、全局直连、仅代理国内；SSTap 规则文件；内置广告拦截规则（geosite:category-ads-all → 阻断）
- **真实流量统计**：从内核 stats 计数器每秒采样，实时速率与累计流量
- **系统代理**：一键接管/还原 Windows 系统代理；应用退出时自动还原，崩溃残留的代理设置会在下次启动时清理，不留死代理
- **网页授权登录**：本地回调服务使用一次性随机回调路径 + state 参数，防登录 CSRF；登录凭证经 Windows DPAPI 加密落盘
- **持久化**：节点、订阅、设置、累计流量保存至 `%APPDATA%\KNcloud\config.json`，重启自动恢复
- **端口冲突检测**：内核启动前预检端口占用并给出中文提示（可在首选项中修改端口）

## 构建

环境要求：

- **Go ≥ 1.26**（Xray-core v26.3.27 硬性要求，低版本编译会提示 toolchain 升级）
- **Node.js ≥ 24**（前端构建）
- **Wails CLI v2.15.0**：`go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0`
- Windows 10/11（WebView2 运行时随系统分发；TUN 模式需管理员权限）

```bash
wails build        # 产物位于 build/bin/KNcloud-WIN.exe
```

开发模式：`wails dev`（前端热更新，Go 方法可在 http://localhost:34115 调试）。

本地验证：`go vet ./...`、`go test ./...`（Windows 下运行；`KNcloud-WIN.exe --tun-selftest` 可对 TUN 链路做完整自检，需管理员）。

## 说明

- Hysteria2 节点可导入展示，但 Xray 内核不支持其代理转发，启动该类节点会给出明确错误
- 默认端口 SOCKS5 `10808` / HTTP `10809`，与其他代理软件冲突时请在「首选项设置」中修改
- 首选项「自动启动内核」开启时，应用启动即自动恢复上次的节点与系统代理状态；「开机自动启动」在管理员会话下通过任务计划程序实现登录静默启动，普通会话下写 Run 键
- MUX 多路复用默认关闭（与 v2rayN 一致）：XTLS Vision 节点与 mux 不兼容；节点带 flow 时即使开启 mux 也会自动跳过
- 分享链接的 `allowInsecure=1` 会被解析并透传内核（自签证书节点用），未指定时保持证书校验
- 允许局域网连接（Allow LAN）时入站监听 0.0.0.0 且无认证，请仅在可信网络中开启
