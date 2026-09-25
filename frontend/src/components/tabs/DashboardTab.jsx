import { ArrowDownLeft, ArrowUpRight, Radio, RefreshCw, Server, Zap } from 'lucide-react';

export default function DashboardTab({ account, fmtGB, handleLogout, handleRoutingChange, handleSelectNode, handleToggleTun, handleUpdateSubscription, nodes, quickPickNodes, setActiveTab, settings, status, subscriptions, syncing, tunBusy, tunPending }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
      <div className="content-header">
        <div>
          <h1 className="content-title">运行状态概览</h1>
        </div>
      </div>

      {account && account.loggedIn && (
        <div className="win11-card account-card">
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <h4 style={{ fontSize: '15px', fontWeight: 600, color: 'var(--text-primary)' }}>
                  {account.email}
                </h4>
                <span style={{ fontSize: '12px', color: 'var(--accent)', fontWeight: 600, padding: '2px 8px', borderRadius: '6px', background: 'var(--accent-subtle)' }}>
                  {account.planName || 'KNcloud 会员'}
                </span>
              </div>
              <div style={{ fontSize: '12px', color: 'var(--text-secondary)', marginTop: '6px' }}>
                已用 {fmtGB(account.usedUp + account.usedDown)} / {account.transferEnable > 0 ? fmtGB(account.transferEnable) : '无限'} · {account.expire}
                {subscriptions[0] && (
                  <span style={{ marginLeft: '12px', color: 'var(--text-tertiary)' }}>
                    上次同步: {subscriptions[0].updatedAt}
                  </span>
                )}
              </div>
            </div>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button className="win11-btn" onClick={handleUpdateSubscription} disabled={syncing}>
                <RefreshCw size={13} className={syncing ? 'spin' : ''} />
                <span>{syncing ? '更新中...' : '更新订阅'}</span>
              </button>
              <button className="win11-btn danger" onClick={handleLogout}>
                <span>退出登录</span>
              </button>
            </div>
          </div>
          <div className="simple-account-bar" style={{ marginTop: '14px' }}>
            <div style={{
              width: account.transferEnable > 0
                ? Math.min(100, Math.round((account.usedUp + account.usedDown) * 100 / account.transferEnable)) + '%'
                : '0%'
            }} />
          </div>
        </div>
      )}


      {/* Top Hero Status Banner：仅展示当前连接节点 + 策略/TUN 控制 */}
      <div className="win11-card" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '24px' }}>
        <div>
          <h2 style={{ fontSize: '18px', fontWeight: 600, color: 'var(--text-primary)' }}>
            {status.activeNodeName === '未选择节点' ? '未选择节点' : `当前节点：${status.activeNodeName}`}
          </h2>
          <p style={{ fontSize: '13px', color: 'var(--text-secondary)', marginTop: '4px' }}>
            {status.activeNodeName === '未选择节点'
              ? '请在服务器节点列表中选择一个节点'
              : `${status.activeNodeProto}${status.tunnelMode ? ' · TUN 分流（大陆直连）' : ''}`}
          </p>
        </div>

        {/* Routing policy radio + TUN mode switch */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '24px' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
            <span style={{ fontSize: '12px', color: 'var(--text-secondary)' }}>分流策略</span>
            <div
              className="segmented-control"
              style={tunBusy || !!tunPending ? { opacity: 0.45, pointerEvents: 'none' } : null}
            >
              <button
                className={`segment-btn ${status.routingMode === 'bypass-cn' ? 'active' : ''}`}
                onClick={() => handleRoutingChange('bypass-cn')}
              >
                绕过大陆
              </button>
              <button
                className={`segment-btn ${status.routingMode === 'global' ? 'active' : ''}`}
                onClick={() => handleRoutingChange('global')}
              >
                全局代理
              </button>
              <button
                className={`segment-btn ${status.routingMode === 'direct' ? 'active' : ''}`}
                onClick={() => handleRoutingChange('direct')}
              >
                全局直连
              </button>
            </div>
          </div>

          <div style={{ width: '1px', height: '40px', background: 'var(--border-subtle)' }} />

          <div style={{ display: 'flex', flexDirection: 'column', gap: '6px', width: '250px', flexShrink: 0 }}>
            <span style={{ fontSize: '12px', color: 'var(--text-secondary)' }}>TUN 模式</span>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '10px' }}>
              <span style={{
                fontSize: '11px',
                color: 'var(--text-tertiary)',
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                flex: 1
              }}>
                {tunPending === 'on'
                  ? '正在启动 TUN…'
                  : tunPending === 'off'
                    ? '正在关闭 TUN…'
                    : status.tunnelMode
                      ? '虚拟网卡接管全部流量 · 大陆直连'
                      : '虚拟网卡接管全部流量（需管理员）'}
              </span>
              <label className="win11-toggle" style={{ flexShrink: 0 }} title="开启后停用内核代理，由 TUN 虚拟网卡接管系统全部流量">
                <input
                  type="checkbox"
                  checked={tunPending ? tunPending === 'on' : !!status.tunnelMode}
                  disabled={tunBusy}
                  onChange={e => handleToggleTun(e.target.checked)}
                />
                <span className="toggle-track"><span className="toggle-thumb" /></span>
              </label>
            </div>
          </div>
        </div>
      </div>

      {/* 4 Metric Cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: '16px' }}>
        <div className="win11-card">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', color: 'var(--text-secondary)', fontSize: '12px' }}>
            <span>实时上传</span>
            <ArrowUpRight size={16} color="#0078d4" />
          </div>
          <div style={{ fontSize: '22px', fontWeight: 700, marginTop: '8px', color: 'var(--text-primary)' }}>
            {status.upSpeed}
          </div>
          <div style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginTop: '4px' }}>
            累计上行: {status.totalUp}
          </div>
        </div>

        <div className="win11-card">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', color: 'var(--text-secondary)', fontSize: '12px' }}>
            <span>实时下载</span>
            <ArrowDownLeft size={16} color="#107c41" />
          </div>
          <div style={{ fontSize: '22px', fontWeight: 700, marginTop: '8px', color: 'var(--text-primary)' }}>
            {status.downSpeed}
          </div>
          <div style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginTop: '4px' }}>
            累计下行: {status.totalDown}
          </div>
        </div>

        <div className="win11-card">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', color: 'var(--text-secondary)', fontSize: '12px' }}>
            <span>Windows 系统代理</span>
            <Radio size={16} color={status.systemProxy ? '#107c41' : '#888'} />
          </div>
          <div style={{ fontSize: '17px', fontWeight: 600, marginTop: '10px', color: status.systemProxy ? 'var(--accent)' : 'var(--text-secondary)' }}>
            {status.systemProxy ? '已接管 (127.0.0.1)' : '未接管 (直连)'}
          </div>
          <div style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginTop: '6px' }}>
            端口: 127.0.0.1:{settings.httpPort}
          </div>
        </div>

        <div className="win11-card">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', color: 'var(--text-secondary)', fontSize: '12px' }}>
            <span>已载入服务器</span>
            <Server size={16} color="#9b59b6" />
          </div>
          <div style={{ fontSize: '22px', fontWeight: 700, marginTop: '8px', color: 'var(--text-primary)' }}>
            {nodes.length} 个节点
          </div>
          <div style={{ fontSize: '11px', color: 'var(--text-tertiary)', marginTop: '4px' }}>
            覆盖 {subscriptions.length} 个订阅源
          </div>
        </div>
      </div>

      {/* Quick Node Switcher in Dashboard */}
      <div className="win11-card">
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '14px' }}>
          <h3 style={{ fontSize: '14px', fontWeight: 600, color: 'var(--text-primary)' }}>推荐节点快速选择</h3>
          <button className="win11-btn" onClick={() => setActiveTab('servers')}>
            查看全部节点 ({nodes.length})
          </button>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px' }}>
          {quickPickNodes.slice(0, 3).map(node => (
            <div
              key={node.id}
              onClick={() => handleSelectNode(node.id)}
              style={{
                padding: '12px 14px',
                borderRadius: '6px',
                border: node.active ? '2px solid var(--accent)' : '1px solid var(--border-subtle)',
                background: node.active ? 'var(--accent-subtle)' : 'var(--bg-card)',
                cursor: 'pointer',
                display: 'flex',
                flexDirection: 'column',
                gap: '6px',
                transition: 'all 0.15s ease'
              }}
            >
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span className={`proto-badge proto-${node.protocol.toLowerCase()}`}>{node.protocol}</span>
                {(node.delay > 0 || node.delay === -2) && (
                  <span className={`latency-pill ${node.delay > 0 && node.delay < 300 ? 'latency-good' : node.delay < 800 ? 'latency-medium' : 'latency-none'}`}>
                    <Zap size={12} /> {node.delay > 0 ? `${node.delay} ms` : '超时'}
                  </span>
                )}
              </div>
              <div style={{ fontWeight: 600, fontSize: '13px', color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {node.name}
              </div>
              <div style={{ fontSize: '11px', color: 'var(--text-tertiary)' }}>
                {node.address}:{node.port}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
