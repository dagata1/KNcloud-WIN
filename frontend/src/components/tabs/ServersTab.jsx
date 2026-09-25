import { Gauge, Pencil, Plus, RefreshCw, Trash2, Zap } from 'lucide-react';

export default function ServersTab({ deletingSelected, filteredNodes, handleDeleteNode, handleDeleteSelected, handlePingAll, handlePingSelected, handlePingSingleNode, handleSelectNode, isPingingAll, pingProgress, openEditNode, selectedNodeIds, setSelectedNodeIds, setShowAddNodeModal, showToast, switchingNodeId }) {
  // 批量测速期间显示实时进度，避免几十秒毫无反馈
  const pingLabel = pingProgress && pingProgress.total > 0
    ? `测速中 ${pingProgress.done}/${pingProgress.total}`
    : '测速中…';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
      <div className="content-header" style={{ marginBottom: '4px' }}>
        <div>
          <h1 className="content-title">节点列表</h1>
          {selectedNodeIds.length > 0 ? (
            <p className="content-subtitle">
              已选中 {selectedNodeIds.length} 个节点 · 按 <kbd style={{ background: "var(--bg-card)", padding: "1px 5px", borderRadius: "3px", border: "1px solid var(--border-subtle)" }}>Ctrl+R</kbd> 批量测速 · 按 <kbd style={{ background: 'var(--bg-card)', padding: '1px 5px', borderRadius: '3px', border: '1px solid var(--border-subtle)' }}>Esc</kbd> 取消选择
            </p>
          ) : (
            <p className="content-subtitle">
              快捷键：<kbd style={{ background: 'var(--bg-card)', padding: '1px 5px', borderRadius: '3px', border: '1px solid var(--border-subtle)' }}>Ctrl+A</kbd> 全选 · <kbd style={{ background: 'var(--bg-card)', padding: '1px 5px', borderRadius: '3px', border: '1px solid var(--border-subtle)' }}>Ctrl+R</kbd> 测速 · <kbd style={{ background: 'var(--bg-card)', padding: '1px 5px', borderRadius: '3px', border: '1px solid var(--border-subtle)' }}>Ctrl+点击</kbd> 多选
            </p>
          )}
        </div>
        <div style={{ display: 'flex', gap: '8px' }}>
          {selectedNodeIds.length > 0 ? (
            <>
              <button
                className="win11-btn primary"
                onClick={handlePingSelected}
                disabled={isPingingAll}
                title="真连接测速所有选中的节点（快捷键 Ctrl+R）"
              >
                <Gauge size={13} className={isPingingAll ? 'spin' : ''} />
                <span>{isPingingAll ? pingLabel : `测速选中 (${selectedNodeIds.length})`}</span>
              </button>
              <button
                className="win11-btn"
                onClick={handleDeleteSelected}
                disabled={deletingSelected}
                title="删除所有选中的节点"
              >
                <Trash2 size={13} />
                <span>{deletingSelected ? '删除中…' : `删除选中 (${selectedNodeIds.length})`}</span>
              </button>
              <button
                className="win11-btn"
                onClick={() => setSelectedNodeIds([])}
                title="取消多选（快捷键 Esc）"
              >
                <span>取消选择</span>
              </button>
            </>
          ) : (
            <>
              <button
                className="win11-btn"
                onClick={() => {
                  const allIds = filteredNodes.map(n => n.id);
                  setSelectedNodeIds(allIds);
                  showToast(`已全选 ${allIds.length} 个节点（按 Ctrl+R 测速）`, 'info');
                }}
                title="全选当前列表节点（快捷键 Ctrl+A）"
              >
                <span>全选节点</span>
              </button>
              <button
                className="win11-btn"
                onClick={handlePingAll}
                disabled={isPingingAll}
                title="全部节点真连接测速"
              >
                <Zap size={13} className={isPingingAll ? 'spin' : ''} />
                <span>{isPingingAll ? pingLabel : '全部测速'}</span>
              </button>
            </>
          )}
          <button className="win11-btn primary" onClick={() => setShowAddNodeModal(true)} title="手动添加单个节点">
            <Plus size={13} />
            <span>添加节点</span>
          </button>
        </div>
      </div>

      {/* Nodes List */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', marginTop: '6px' }}>
        {filteredNodes.map(node => {
          const isSwitching = switchingNodeId === node.id;
          const isSelected = selectedNodeIds.includes(node.id);
          const isActive = !!node.active;
          return (
            <div
              key={node.id}
              className="win11-card"
              onClick={(e) => {
                if (e.ctrlKey || e.metaKey) {
                  // Ctrl+点击：加选/减选，不改变当前连接节点
                  setSelectedNodeIds(ids => ids.includes(node.id)
                    ? ids.filter(x => x !== node.id)
                    : [...ids, node.id]);
                  return;
                }
                if (e.shiftKey && selectedNodeIds.length > 0) {
                  // Shift+点击：范围多选
                  const lastId = selectedNodeIds[selectedNodeIds.length - 1];
                  const lastIdx = filteredNodes.findIndex(n => n.id === lastId);
                  const curIdx = filteredNodes.findIndex(n => n.id === node.id);
                  if (lastIdx !== -1 && curIdx !== -1) {
                    const [start, end] = lastIdx < curIdx ? [lastIdx, curIdx] : [curIdx, lastIdx];
                    const rangeIds = filteredNodes.slice(start, end + 1).map(n => n.id);
                    setSelectedNodeIds(Array.from(new Set([...selectedNodeIds, ...rangeIds])));
                    return;
                  }
                }
                setSelectedNodeIds([node.id]);
                handleSelectNode(node.id);
              }}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '12px 18px',
                cursor: 'pointer',
                border: (isActive || isSelected)
                  ? '1px solid var(--accent)'
                  : '1px solid var(--border-subtle)',
                boxShadow: (isActive || isSelected)
                  ? '0 0 0 1px var(--accent)'
                  : 'var(--shadow-card)',
                background: isActive
                  ? 'var(--accent-subtle)'
                  : isSelected
                    ? 'rgba(0, 120, 212, 0.08)'
                    : 'var(--bg-card)',
                transition: 'all 0.15s ease',
                // 不可用节点整行淡化，与可用节点一眼区分
                opacity: node.unsupported ? 0.55 : 1
              }}
              title={node.unsupported || undefined}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: '14px', flex: 1 }}>
                <div style={{
                  width: '18px',
                  height: '18px',
                  borderRadius: '50%',
                  border: isActive
                    ? '5px solid var(--accent)'
                    : isSelected
                      ? '4px solid var(--accent)'
                      : '2px solid var(--border-default)',
                  backgroundColor: 'transparent',
                  transition: 'border 0.15s ease'
                }} />
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <span className={`proto-badge proto-${node.protocol.toLowerCase()}`}>{node.protocol}</span>
                    <strong style={{ fontSize: '13px', color: 'var(--text-primary)' }}>{node.name}</strong>
                    {node.active && (
                      <span style={{
                        fontSize: '10px',
                        fontWeight: 600,
                        color: 'var(--accent)',
                        background: 'var(--accent-subtle)',
                        border: '1px solid var(--accent-border)',
                        padding: '1px 6px',
                        borderRadius: '3px',
                        display: 'inline-flex',
                        alignItems: 'center',
                        lineHeight: '1.2'
                      }}>
                        活动
                      </span>
                    )}
                    {isSwitching && (
                      <span style={{ fontSize: '11px', color: 'var(--accent)', display: 'flex', alignItems: 'center', gap: '4px' }}>
                        <RefreshCw size={11} className="spin" /> 切换中…
                      </span>
                    )}
                    {node.unsupported && (
                      <span
                        title={node.unsupported}
                        style={{
                          fontSize: '10px',
                          fontWeight: 600,
                          color: 'var(--danger, #c42b1c)',
                          background: 'rgba(196, 43, 28, 0.10)',
                          border: '1px solid rgba(196, 43, 28, 0.35)',
                          padding: '1px 6px',
                          borderRadius: '3px',
                          display: 'inline-flex',
                          alignItems: 'center',
                          lineHeight: '1.2'
                        }}
                      >
                        不支持
                      </span>
                    )}
                  </div>
                  <div style={{ fontSize: '11px', color: 'var(--text-secondary)', marginTop: '4px' }}>
                    {node.address}:{node.port} · 安全: {node.security} · 传输: {node.network}
                  </div>
                  {node.unsupported && (
                    <div style={{ fontSize: '11px', color: 'var(--danger, #c42b1c)', marginTop: '3px' }}>
                      {node.unsupported}
                    </div>
                  )}
                </div>
              </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: '20px' }}>
              {(node.delay > 0 || node.delay === -2) && (
                <span className={`latency-pill ${node.delay > 0 && node.delay < 300 ? 'latency-good' : node.delay < 800 ? 'latency-medium' : 'latency-none'}`} style={{ fontSize: '12px' }}>
                  <Zap size={13} />
                  {node.delay > 0 ? `${node.delay} ms` : '超时'}
                </span>
              )}

              <div style={{ display: 'flex', gap: '6px' }}>
                <button
                  className="win11-btn"
                  style={{ height: '28px', padding: '0 8px' }}
                  onClick={(e) => openEditNode(node, e)}
                  title="编辑节点"
                >
                  <Pencil size={12} />
                </button>
                <button
                  className="win11-btn"
                  style={{ height: '28px', padding: '0 8px' }}
                  onClick={(e) => handlePingSingleNode(node.id, e)}
                  title="真连接测速"
                >
                  <Gauge size={12} />
                </button>
                <button
                  className="win11-btn danger"
                  style={{ height: '28px', padding: '0 8px' }}
                  onClick={(e) => handleDeleteNode(node.id, e)}
                  title="删除节点"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            </div>
          </div>
        );
      })}
      </div>
    </div>
  );
}
