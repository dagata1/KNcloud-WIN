import { ChevronDown, Gauge, LoaderCircle, Minus, Moon, Power, RefreshCw, SlidersHorizontal, Square, Sun, X } from 'lucide-react';
import { WindowClose, WindowMax, WindowMin } from '../../wailsjs/go/main/App';

export default function SimpleView({ account, brandLogo, closeWindowTitle, delayColor, delayText, fmtGB, handleLogout, handlePingAll, handleSimpleConnect, handleSimpleSelectNode, handleThemeToggle, handleUiModeToggle, handleUpdateSubscription, isPingingAll, nodeMenuOpen, nodes, renderToasts, setNodeMenuOpen, simpleNet, status, syncing, theme }) {
  const activeNode = nodes.find(n => n.active);
  // 简易模式的状态完全由分流策略决定（不再依赖 TUN 是否运行）
  const routing = status.routingMode || 'bypass-cn';
  const simpleOn = routing === 'bypass-cn';
  return (
    <div className={`app-window ${theme === 'dark' ? 'dark-theme' : ''}`}>
      <header className="titlebar drag-region">
        <div className="titlebar-left">
          <img src={brandLogo} alt="KNcloud-WIN" style={{ height: "22px", width: "auto", display: "block" }} />
        </div>
        <div className="titlebar-right no-drag">
          <button className="theme-toggle-btn" onClick={handleUiModeToggle} title="切换到普通模式（完整设置）">
            <SlidersHorizontal size={15} />
          </button>
          <button className="theme-toggle-btn" onClick={handleThemeToggle} title="切换浅色 / 深色主题">
            {theme === 'dark' ? <Sun size={15} /> : <Moon size={15} />}
          </button>
          <button className="win-caption-btn" onClick={() => WindowMin()} title="最小化">
            <Minus size={13} />
          </button>
          <button className="win-caption-btn" onClick={() => WindowMax()} title="最大化">
            <Square size={11} />
          </button>
          <button className="win-caption-btn btn-close" onClick={() => WindowClose()} title={closeWindowTitle}>
            <X size={14} />
          </button>
        </div>
      </header>

      {renderToasts()}
      <div className="simple-body">
        <button
          className={[
            'simple-power-btn',
            simpleOn && simpleNet === 'ok' ? 'connected' : '',
            simpleOn && simpleNet === 'checking' ? 'connecting' : '',
            simpleOn && simpleNet === 'fail' ? 'failed' : '',
          ].filter(Boolean).join(' ')}
          onClick={handleSimpleConnect}
          title={simpleOn ? '点击切换到全局直连' : '点击开启分流代理（绕过大陆）'}
        >
          {simpleOn && simpleNet === 'checking'
            ? <LoaderCircle size={56} className="spin" />
            : <Power size={56} />}
        </button>

        <div
          className={`simple-status-text ${simpleOn && simpleNet === 'checking' ? 'checking' : ''}`}
          style={simpleNet === 'fail' ? { color: '#ff6b6b' } : (simpleOn && simpleNet === 'ok' ? { color: '#3fbf6f' } : null)}
        >
          {!simpleOn
            ? '开始连接'
            : simpleNet === 'checking'
              ? '正在连接…'
              : simpleNet === 'fail'
                ? '连接失败，请更换节点'
                : '链接成功'}
        </div>

        <div className="simple-speed">
          <span>↑ {status.upSpeed}</span>
          <span>↓ {status.downSpeed}</span>
        </div>

        <div className="simple-node-area" style={{ position: 'relative' }}>
          <label>代理节点</label>
          <button type="button" className="simple-node-trigger" onClick={() => setNodeMenuOpen(o => !o)}>
            {activeNode ? (
              <>
                <span className={`proto-badge proto-${activeNode.protocol.toLowerCase()}`}>{activeNode.protocol}</span>
                <span className="simple-node-name">{activeNode.name}</span>
                <span className="simple-node-delay" style={{ color: delayColor(activeNode.delay) }}>
                  {delayText(activeNode.delay)}
                </span>
              </>
            ) : (
              <span className="simple-node-name">暂无节点，请在普通模式中添加</span>
            )}
            <ChevronDown
              size={14}
              style={{ flexShrink: 0, opacity: 0.6, transition: 'transform .15s', transform: nodeMenuOpen ? 'rotate(180deg)' : 'none' }}
            />
          </button>

          {nodeMenuOpen && (
            <>
              <div style={{ position: 'fixed', inset: 0, zIndex: 90 }} onClick={() => setNodeMenuOpen(false)} />
              <div className="simple-node-menu">
                {nodes.length === 0 && <div className="simple-node-empty">暂无节点，请在普通模式中添加</div>}
                {nodes.map(n => (
                  <div
                    key={n.id}
                    className={`simple-node-item ${activeNode && n.id === activeNode.id ? 'active' : ''}`}
                    onClick={() => {
                      setNodeMenuOpen(false);
                      if (!activeNode || n.id !== activeNode.id) handleSimpleSelectNode(n.id);
                    }}
                  >
                    <span className={`proto-badge proto-${n.protocol.toLowerCase()}`}>{n.protocol}</span>
                    <span className="simple-node-name">{n.name}</span>
                    <span className="simple-node-delay" style={{ color: delayColor(n.delay) }}>
                      {delayText(n.delay)}
                    </span>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>

        {account && account.loggedIn && (
          <div className="simple-account">
            <div className="simple-account-row">
              <span>{account.email}</span>
              <span>{account.planName || 'KNcloud 套餐'}</span>
            </div>
            <div className="simple-account-bar">
              <div style={{
                width: account.transferEnable > 0
                  ? Math.min(100, Math.round((account.usedUp + account.usedDown) * 100 / account.transferEnable)) + '%'
                  : '0%'
              }} />
            </div>
            <div className="simple-account-row small">
              <span>已用 {fmtGB(account.usedUp + account.usedDown)} / {account.transferEnable > 0 ? fmtGB(account.transferEnable) : '无限'}</span>
              <span>{account.expire}</span>
            </div>
            <div className="simple-account-actions">
              <button className="win11-btn" onClick={handleUpdateSubscription} disabled={syncing}>
                <RefreshCw size={13} className={syncing ? 'spin' : ''} />
                <span>{syncing ? '更新中…' : '更新订阅'}</span>
              </button>
              <button className="win11-btn" onClick={handlePingAll} disabled={isPingingAll}>
                <Gauge size={13} />
                <span>{isPingingAll ? '测速中…' : '测试节点'}</span>
              </button>
              <button className="win11-btn danger" onClick={handleLogout}>
                <span>退出登录</span>
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
