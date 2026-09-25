import { RefreshCw, Trash2 } from 'lucide-react';
import { ClearLogs } from '../../../wailsjs/go/main/App';

export default function LogsTab({ logs, refreshAllData }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '14px', height: '100%' }}>
      <div className="content-header" style={{ marginBottom: 0 }}>
        <div>
          <h1 className="content-title">实时运行日志</h1>
          <p className="content-subtitle">查看代理核心的连接追踪、分流判定与警告异常</p>
        </div>
        <div style={{ display: 'flex', gap: '8px' }}>
          <button className="win11-btn" onClick={() => ClearLogs()}>
            <Trash2 size={13} />
            <span>清空日志</span>
          </button>
          <button className="win11-btn primary" onClick={() => refreshAllData()}>
            <RefreshCw size={13} />
            <span>刷新</span>
          </button>
        </div>
      </div>

      <div className="win11-card" style={{ flex: 1, padding: '14px', background: 'rgba(15, 15, 15, 0.85)', color: '#eaeaea', fontFamily: 'Consolas, monospace', fontSize: '12px', overflowY: 'auto', borderRadius: '8px', minHeight: '380px' }}>
        {logs.map(log => (
          <div key={log.id} style={{ display: 'flex', gap: '10px', lineHeight: '1.7' }}>
            <span style={{ color: '#888' }}>[{log.time}]</span>
            <span style={{
              color: log.level === 'error' ? '#f85149' : log.level === 'warn' ? '#d29922' : '#58a6ff',
              fontWeight: 600,
              width: '45px'
            }}>
              {log.level.toUpperCase()}
            </span>
            <span style={{ color: '#e6edf3' }}>{log.message}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
