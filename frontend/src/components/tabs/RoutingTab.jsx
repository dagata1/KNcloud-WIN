

export default function RoutingTab({ handleRoutingChange, status }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '18px' }}>
      <div className="content-header">
        <div>
          <h1 className="content-title">路由与分流规则</h1>
          <p className="content-subtitle">智能域名解析与流量分流（基于 GEOIP & GEOSITE）</p>
        </div>
      </div>

      <div className="win11-card" style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
        <h3 style={{ fontSize: '14px', fontWeight: 600 }}>全局路由模式</h3>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px' }}>
          <div
            onClick={() => handleRoutingChange('bypass-cn')}
            style={{
              padding: '16px',
              borderRadius: '8px',
              cursor: 'pointer',
              border: status.routingMode === 'bypass-cn' ? '2px solid var(--accent)' : '1px solid var(--border-subtle)',
              background: status.routingMode === 'bypass-cn' ? 'var(--accent-subtle)' : 'var(--bg-card)'
            }}
          >
            <h4 style={{ fontSize: '13px', fontWeight: 600 }}>绕过大陆 (GFWList)</h4>
            <p style={{ fontSize: '11px', color: 'var(--text-secondary)', marginTop: '4px' }}>
              国内 IP 与主流国内域名直连，被屏蔽的国际网站自动通过节点代理转发。
            </p>
          </div>

          <div
            onClick={() => handleRoutingChange('global')}
            style={{
              padding: '16px',
              borderRadius: '8px',
              cursor: 'pointer',
              border: status.routingMode === 'global' ? '2px solid var(--accent)' : '1px solid var(--border-subtle)',
              background: status.routingMode === 'global' ? 'var(--accent-subtle)' : 'var(--bg-card)'
            }}
          >
            <h4 style={{ fontSize: '13px', fontWeight: 600 }}>全局代理 (Global)</h4>
            <p style={{ fontSize: '11px', color: 'var(--text-secondary)', marginTop: '4px' }}>
              全部外部流量强制通过当前选中节点代理。
            </p>
          </div>

          <div
            onClick={() => handleRoutingChange('direct')}
            style={{
              padding: '16px',
              borderRadius: '8px',
              cursor: 'pointer',
              border: status.routingMode === 'direct' ? '2px solid var(--accent)' : '1px solid var(--border-subtle)',
              background: status.routingMode === 'direct' ? 'var(--accent-subtle)' : 'var(--bg-card)'
            }}
          >
            <h4 style={{ fontSize: '13px', fontWeight: 600 }}>全局直连 (Direct)</h4>
            <p style={{ fontSize: '11px', color: 'var(--text-secondary)', marginTop: '4px' }}>
              不通过代理服务器，直接使用本地宽带直连访问互联网。
            </p>
          </div>
        </div>
      </div>

      {/* Domain Rule Cards */}
      <div className="win11-card" style={{ display: 'flex', flexDirection: 'column', gap: '14px' }}>
        <h3 style={{ fontSize: '14px', fontWeight: 600 }}>预设规则集开关</h3>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
          {[
            { title: '绕过局域网私有网段 (Private IPs)', desc: '10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 走直连', active: true },
            { title: '拦截常见广告与恶意跟踪追踪 (AdBlock)', desc: 'geosite:category-ads-all 直接阻断 (Block)', active: true },
            { title: 'AI 智能助手服务优化 (OpenAI, Claude, Gemini)', desc: '强制锁定特定专线低延迟节点路由', active: true },
            { title: '国外流媒体解锁路由 (Netflix / Disney+)', desc: '自动匹配支持原生 IP 解锁的目标节点', active: false }
          ].map((rule, idx) => (
            <div key={idx} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '10px 0', borderBottom: '1px solid var(--border-subtle)' }}>
              <div>
                <div style={{ fontSize: '13px', fontWeight: 500, color: 'var(--text-primary)' }}>{rule.title}</div>
                <div style={{ fontSize: '11px', color: 'var(--text-secondary)', marginTop: '2px' }}>{rule.desc}</div>
              </div>
              <label className="win11-toggle">
                <input type="checkbox" defaultChecked={rule.active} />
                <span className="toggle-track"><span className="toggle-thumb" /></span>
              </label>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
