import { Globe, Minus, Square, X } from 'lucide-react';
import { WindowClose, WindowMax, WindowMin } from '../../wailsjs/go/main/App';

export default function LoginView({ handleCancelWebLogin, handleLogin, handleWebLogin, loginBusy, loginErr, loginForm, loginLogo, renderToasts, setLoginForm, theme, webLoginWaiting }) {
  return (
    <div className={`app-window ${theme === 'dark' ? 'dark-theme' : ''}`}>
      <header className="titlebar drag-region">
        <div className="titlebar-left" />
        <div className="titlebar-right no-drag">
          <button className="win-caption-btn" onClick={() => WindowMin()} title="最小化"><Minus size={13} /></button>
          <button className="win-caption-btn" onClick={() => WindowMax()} title="最大化"><Square size={11} /></button>
          <button className="win-caption-btn btn-close" onClick={() => WindowClose()} title="关闭"><X size={14} /></button>
        </div>
      </header>
      {renderToasts()}
      <div className="login-body">
        <img src={loginLogo} alt="" className="login-logo" style={{ height: "64px", width: "auto", objectFit: "contain" }} />
        <input
          type="email"
          className="win11-input login-input"
          placeholder="邮箱地址"
          value={loginForm.email}
          onChange={e => setLoginForm({ ...loginForm, email: e.target.value })}
        />
        <input
          type="password"
          className="win11-input login-input"
          placeholder="密码"
          value={loginForm.password}
          onChange={e => setLoginForm({ ...loginForm, password: e.target.value })}
          onKeyDown={e => { if (e.key === 'Enter') handleLogin(); }}
        />
        {loginErr && <div className="login-err">{loginErr}</div>}
        <button className="win11-btn primary login-btn" disabled={loginBusy || webLoginWaiting} onClick={handleLogin}>
          {loginBusy ? '登录中…' : '登 录'}
        </button>
        <div className="login-divider"><span>或</span></div>
        <button className="win11-btn login-btn" disabled={loginBusy || webLoginWaiting} onClick={handleWebLogin}>
          <Globe size={14} />
          <span>{webLoginWaiting ? '等待网页授权…' : '通过网站登录'}</span>
        </button>
        {/* 等待授权时给出退出口：否则用户只能干等后端 5 分钟超时 */}
        {webLoginWaiting && (
          <button className="win11-btn login-btn" onClick={handleCancelWebLogin}>
            <X size={14} />
            <span>取消网页登录</span>
          </button>
        )}
      </div>
    </div>
  );
}
