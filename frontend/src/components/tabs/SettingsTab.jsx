import { Check } from 'lucide-react';

export default function SettingsTab({ handleSaveSettings, setLocalSettings, settings }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
      <div className="content-header">
        <div>
          <h1 className="content-title">首选项设置</h1>
          <p className="content-subtitle">配置本地监听端口、DNS 解析与系统集成</p>
        </div>
        <button className="win11-btn primary" onClick={handleSaveSettings}>
          <Check size={14} />
          <span>保存设置</span>
        </button>
      </div>

      {/* Group 1 */}
      <div className="win11-card" style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
        <h3 style={{ fontSize: '14px', fontWeight: 600 }}>本地代理端口</h3>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: '16px' }}>
          <div className="form-group">
            <label className="form-label">SOCKS5 代理端口</label>
            <input
              type="number"
              className="win11-input"
              value={settings.socksPort}
              onChange={e => setLocalSettings({ ...settings, socksPort: parseInt(e.target.value, 10) || 10808 })}
            />
          </div>
          <div className="form-group">
            <label className="form-label">HTTP / HTTPS 代理端口</label>
            <input
              type="number"
              className="win11-input"
              value={settings.httpPort}
              onChange={e => setLocalSettings({ ...settings, httpPort: parseInt(e.target.value, 10) || 10809 })}
            />
          </div>
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '6px' }}>
          <div>
            <div style={{ fontSize: '13px', fontWeight: 500 }}>允许来自局域网的连接 (Allow LAN)</div>
            <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>让同局域网设备通过本机 IP 代理上网</div>
          </div>
          <label className="win11-toggle">
            <input
              type="checkbox"
              checked={settings.allowLan}
              onChange={e => setLocalSettings({ ...settings, allowLan: e.target.checked })}
            />
            <span className="toggle-track"><span className="toggle-thumb" /></span>
          </label>
        </div>
      </div>

      {/* Group 2 */}
      <div className="win11-card" style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
        <h3 style={{ fontSize: '14px', fontWeight: 600 }}>核心引擎与多路复用</h3>
        <div className="form-group">
          <label className="form-label">底层 Core 类型</label>
          {/* 目前只内置 Xray-core 一种内核（编译期嵌入）。
              这里曾是可选 sing-box / V2Ray-core 的下拉框，但后端
              startCoreLocked() 永远启动 Xray，选项不产生任何效果 ——
              属于会误导用户的「假开关」，改为只读展示。 */}
          <input
            type="text"
            className="win11-input"
            value={`${settings.coreType || 'Xray-core'}（内置，当前版本不可切换）`}
            readOnly
            disabled
          />
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <div style={{ fontSize: '13px', fontWeight: 500 }}>启用 MUX 多路复用 (Multiplexing)</div>
            <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>合并 TCP 连接，降低握手延迟</div>
          </div>
          <label className="win11-toggle">
            <input
              type="checkbox"
              checked={settings.muxEnabled}
              onChange={e => setLocalSettings({ ...settings, muxEnabled: e.target.checked })}
            />
            <span className="toggle-track"><span className="toggle-thumb" /></span>
          </label>
        </div>
      </div>

      {/* Group 3 */}
      <div className="win11-card" style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
        <h3 style={{ fontSize: '14px', fontWeight: 600 }}>DNS 服务器设置</h3>
        <div className="form-group">
          <label className="form-label">远程与直连 DNS 服务器地址 (英文逗号分隔)</label>
          <input
            type="text"
            className="win11-input"
            value={settings.dnsServers}
            onChange={e => setLocalSettings({ ...settings, dnsServers: e.target.value })}
          />
        </div>
      </div>

      {/* Group 4: 系统托盘与开机启动 */}
      <div className="win11-card" style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
        <h3 style={{ fontSize: '14px', fontWeight: 600 }}>系统托盘与开机启动</h3>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <div style={{ fontSize: '13px', fontWeight: 500 }}>开机自动启动</div>
            <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>
              登录 Windows 后自动启动 KNcloud-WIN，方便随时接管代理
            </div>
          </div>
          <label className="win11-toggle">
            <input
              type="checkbox"
              checked={!!settings.autoStart}
              onChange={e => setLocalSettings({ ...settings, autoStart: e.target.checked })}
            />
            <span className="toggle-track"><span className="toggle-thumb" /></span>
          </label>
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <div style={{ fontSize: '13px', fontWeight: 500 }}>启动时自动连接</div>
            <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>
              程序启动即拉起内核并接管 Windows 系统代理；关闭后由你手动开启
            </div>
          </div>
          <label className="win11-toggle">
            <input
              type="checkbox"
              checked={!!settings.autoConnect}
              onChange={e => setLocalSettings({ ...settings, autoConnect: e.target.checked })}
            />
            <span className="toggle-track"><span className="toggle-thumb" /></span>
          </label>
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <div style={{ fontSize: '13px', fontWeight: 500 }}>关闭窗口时最小化到托盘</div>
            <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>
              关闭主窗口后程序继续在右下角托盘运行；右键托盘图标可切换模式、切换节点或退出
            </div>
          </div>
          <label className="win11-toggle">
            <input
              type="checkbox"
              checked={!!settings.minimizeToTray}
              onChange={e => setLocalSettings({ ...settings, minimizeToTray: e.target.checked })}
            />
            <span className="toggle-track"><span className="toggle-thumb" /></span>
          </label>
        </div>
      </div>
    </div>
  );
}
