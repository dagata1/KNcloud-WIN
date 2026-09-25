import kncFgBlack from './assets/knc-fg-black.png';
import kncFgWhite from './assets/knc-fg-white.png';
import kncLoginDark from './assets/knc-login-dark.png';
import kncLoginLight from './assets/knc-login-light.png';
import React, { useState, useEffect, useRef } from 'react';
import {
  Shield,
  Activity,
  Server,
  GitFork,
  BookOpen,
  Terminal,
  Settings,
  Menu,
  Sun,
  Moon,
  Minus,
  Square,
  X,
  Play,
  Pause,
  RefreshCw,
  Plus,
  Trash2,
  Zap,
  Globe,
  ArrowUpRight,
  ArrowDownLeft,
  CheckCircle2,
  Sliders,
  Radio,
  Search,
  Check,
  Copy,
  Pencil,
  Power,
  ChevronDown,
  LayoutGrid,
  SlidersHorizontal,
  FolderInput,
  LoaderCircle,
  Gauge
} from 'lucide-react';

import {
  GetNodes,
  SelectNode,
  AddNode,
  DeleteNode,
  DeleteNodes,
  PingNode,
  PingAllNodes,
  PingNodes,
  GetCoreStatus,
  ToggleCore,
  SetRoutingMode,
  SimpleConnect,
  GetSubscriptions,
  UpdateNode,
  ImportNodesFromLinks,
  CopyNodeShareLink,
  ImportNodesFromClipboard,
  Login,
  GetAccount,
  RefreshAccount,
  Logout,
  SyncNodes,
  StartWebLogin,
  CancelWebLogin,
  GetLogs,
  ClearLogs,
  GetSettings,
  SaveSettings,
  WindowMin,
  WindowMax,
  WindowClose
} from '../wailsjs/go/main/App';
import { EventsOn, WindowSetSize, WindowUnmaximise } from '../wailsjs/runtime';

import LoginView from './components/LoginView';
import SimpleView from './components/SimpleView';
import DashboardTab from './components/tabs/DashboardTab';
import ServersTab from './components/tabs/ServersTab';
import RoutingTab from './components/tabs/RoutingTab';
import LogsTab from './components/tabs/LogsTab';
import SettingsTab from './components/tabs/SettingsTab';

export default function App() {
  // 简易模式内容少，窗口切到紧凑尺寸；普通模式恢复默认大小
  // （全局最小尺寸在 main.go 里放开了到 380x560，这里的目标值在其之上）
  const WINDOW_SIZE = { simple: { w: 420, h: 640 }, classic: { w: 1120, h: 760 } };
  const applyWindowSize = async (mode) => {
    const s = WINDOW_SIZE[mode] || WINDOW_SIZE.classic;
    // 窗口处于最大化时 SetSize 不生效，会一直停在大尺寸；先还原成普通窗口
    try { await WindowUnmaximise(); } catch (e) { /* 未最大化时忽略 */ }
    WindowSetSize(s.w, s.h);
  };
  // 节点延迟的展示文字与配色（简易模式节点下拉用）
  const delayText = d => d > 0 ? `${d}ms` : d === -2 ? '超时' : '未测';
  const delayColor = d => d > 0
    ? (d < 300 ? '#3fbf6f' : d < 800 ? '#e5a50a' : '#ff6b6b')
    : d === -2 ? '#ff6b6b' : 'var(--text-tertiary)';

  const [theme, setTheme] = useState('dark');
  const brandLogo = theme === 'dark' ? kncLoginDark : kncLoginLight;
  const loginLogo = theme === 'dark' ? kncLoginDark : kncLoginLight;
  const [uiMode, setUiMode] = useState('simple'); // classic=普通模式, simple=简易模式（登录后默认简洁）
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [activeTab, setActiveTab] = useState('dashboard');
  
  // App core states
  const [status, setStatus] = useState({
    running: true,
    coreType: 'Xray-core',
    coreVersion: 'v1.8.24',
    systemProxy: false,
    routingMode: 'bypass-cn',
    upSpeed: '0 B/s',
    downSpeed: '0 B/s',
    totalUp: '0 GB',
    totalDown: '0 GB',
    activeNodeName: '未选择节点',
    activeNodeProto: 'VLESS'
  });

  const [nodes, setNodes] = useState([]);
  // 简易模式连接检测结果：idle=未连接, checking=检测中, ok=节点可用, fail=节点无效
  const [simpleNet, setSimpleNet] = useState('idle');
  // 简易模式自定义节点下拉是否展开
  const [nodeMenuOpen, setNodeMenuOpen] = useState(false);
  const [selectedProto, setSelectedProto] = useState('ALL');
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedNodeIds, setSelectedNodeIds] = useState([]); // 节点列表多选（Ctrl+A / Ctrl+点击 / Shift+点击）
  const [deletingSelected, setDeletingSelected] = useState(false); // 批量删除进行中
  const [switchingNodeId, setSwitchingNodeId] = useState(null); // 正在切换中的节点 ID
  const [isPingingAll, setIsPingingAll] = useState(false);

  // Subscriptions & Logs
  const [subscriptions, setSubscriptions] = useState([]);
  const [logs, setLogs] = useState([]);
  const [settings, setLocalSettings] = useState({
    socksPort: 10808,
    httpPort: 10809,
    autoStart: true,
    allowLan: false,
    muxEnabled: true,
    coreType: 'Xray-core',
    dnsServers: '1.1.1.1, 8.8.8.8, 223.5.5.5',
    minimizeToTray: true
  });

  // Modals
  const [showAddNodeModal, setShowAddNodeModal] = useState(false);
  const [editingNodeId, setEditingNodeId] = useState(null);
  const [newNode, setNewNode] = useState({
    name: '',
    protocol: 'VLESS',
    address: '',
    port: 443,
    uuid: '',
    security: 'reality',
    network: 'tcp',
    group: 'Custom'
  });

  const [showImportModal, setShowImportModal] = useState(false);
  const [importText, setImportText] = useState('');

  // 账户
  // 批量测速进度：done/total，total 为 0 表示当前没有批量任务在跑
  const [pingProgress, setPingProgress] = useState({ done: 0, total: 0 });

  const [account, setAccount] = useState(null); // null = 尚未从后端加载
  const [loginForm, setLoginForm] = useState({ email: '', password: '' });
  const [loginErr, setLoginErr] = useState('');
  const [loginBusy, setLoginBusy] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [webLoginWaiting, setWebLoginWaiting] = useState(false); // 网页授权登录等待中
  const [tunBusy, setTunBusy] = useState(false); // TUN 模式切换进行中
  const [tunPending, setTunPending] = useState(null); // 'on'/'off'：点击后的即时反馈，完成前显示进行中文案

  const fmtGB = (b) => {
    if (!b || b <= 0) return '0 GB';
    if (b >= 1024 ** 3) return (b / 1024 ** 3).toFixed(2) + ' GB';
    return (b / 1024 ** 2).toFixed(0) + ' MB';
  };

  // 应用内 Toast 通知（替代原生 alert）
  const [toasts, setToasts] = useState([]);
  const toastIdRef = useRef(0);
  const showToast = (msg, type = 'info') => {
    const id = ++toastIdRef.current;
    setToasts(list => [...list, { id, msg, type }]);
    setTimeout(() => {
      setToasts(list => list.filter(t => t.id !== id));
    }, 3500);
  };
  const renderToasts = () => (
    <div className="toast-container">
      {toasts.map(t => (
        <div key={t.id} className={`toast toast-${t.type}`}>{t.msg}</div>
      ))}
    </div>
  );

  // 导入后提示有多少节点当前内核连不上（主要是 Hysteria2）。
  // 后端只写日志，用户不会去翻；不在这里说清楚，他们会等到点击连接才发现。
  // 主操作完成后回读真实状态。这些 getter 极少失败，但一旦失败不应抛出
  // 未处理的 Promise 拒绝 —— 2 秒一次的轮询会把状态补回来。
  const refreshStatus = async () => {
    try {
      setStatus(await GetCoreStatus());
    } catch { /* 交给轮询兜底 */ }
  };
  const refreshNodesAndStatus = async () => {
    try {
      setNodes(await GetNodes());
      setStatus(await GetCoreStatus());
    } catch { /* 交给轮询兜底 */ }
  };

  const warnUnsupported = (list) => {
    const n = (list || []).filter(x => x.unsupported).length;
    if (n > 0) {
      showToast(`其中 ${n} 个节点当前无法使用：内置内核不支持其协议`, 'warn');
    }
  };

  // Load initial data
  useEffect(() => {
    refreshAllData();
    const interval = setInterval(() => {
      fetchStatusAndLogs();
    }, 2000);
    return () => clearInterval(interval);
  }, []);

  // 托盘菜单里的操作（切模式、切节点、开关系统代理等）会通知界面同步刷新
  useEffect(() => {
    const off = EventsOn('kncloud:refresh', () => {
      refreshAllData();
    });
    return () => {
      if (typeof off === 'function') off();
    };
  }, []);

  // 测速结果逐节点推送：后端每测完一个就发一次事件。
  // 批量测速最多 3 个并发，几十个节点要跑十几轮；若等全部结束再一次性刷新，
  // 界面会几十秒毫无反应，看起来像卡死。
  useEffect(() => {
    const off = EventsOn('kncloud:node-delay', (payload) => {
      if (!payload || !payload.id) return;
      setNodes(prev => prev.map(n =>
        n.id === payload.id ? { ...n, delay: payload.delay } : n
      ));
      setPingProgress(p => (p.total > 0 ? { ...p, done: p.done + 1 } : p));
    });
    return () => {
      if (typeof off === 'function') off();
    };
  }, []);

  // 关闭按钮的语义取决于「关闭窗口时最小化到托盘」开关
  const closeWindowTitle = settings.minimizeToTray
    ? '关闭（最小化到系统托盘）'
    : '关闭并退出程序';

  const refreshAllData = async () => {
    try {
      const [curStatus, curNodes, curSubs, curLogs, curSettings, curAccount] = await Promise.all([
        GetCoreStatus(),
        GetNodes(),
        GetSubscriptions(),
        GetLogs(),
        GetSettings(),
        GetAccount()
      ]);
      if (curStatus) setStatus(curStatus);
      if (curNodes) setNodes(curNodes);
      if (curSubs) setSubscriptions(curSubs);
      if (curLogs) setLogs(curLogs);
      if (curAccount) setAccount(curAccount);
      if (curSettings) {
        setLocalSettings(curSettings);
        if (curSettings.theme === 'light') setTheme('light');
        else if (curSettings.theme === 'dark') setTheme('dark');
        // 按持久化的模式 + 登录态统一决定窗口尺寸：
        //   未登录 → 登录页紧凑尺寸；已登录 → 简洁模式紧凑尺寸 / 普通模式默认尺寸
        // （此前只在 uiMode==='simple' 时调整，classic 持久化的简单模式下窗口不会缩小）
        if (curSettings.uiMode === 'simple') setUiMode('simple');
        else if (curSettings.uiMode === 'classic') setUiMode('classic');
        const loggedIn = !!(curAccount && curAccount.loggedIn);
        applyWindowSize(loggedIn
          ? (curSettings.uiMode === 'classic' ? 'classic' : 'simple')
          : 'simple');
      }
    } catch (e) {
      console.error("Init data load error", e);
    }
  };

  const fetchStatusAndLogs = async () => {
    try {
      const curStatus = await GetCoreStatus();
      if (curStatus) setStatus(curStatus);
      const curLogs = await GetLogs();
      if (curLogs) setLogs(curLogs);
    } catch (e) {
      // ignore
    }
  };

  // Actions
  const handleToggleCore = async () => {
    const nextState = !status.running;
    try {
      await ToggleCore(nextState);
    } catch (e) {
      showToast('内核启动失败：' + (e?.message || e), 'error');
    }
    await refreshStatus();
  };

  const handleThemeToggle = async () => {
    const next = theme === 'dark' ? 'light' : 'dark';
    setTheme(next);
    try {
      await SaveSettings({ ...settings, theme: next });
      setLocalSettings(prev => ({ ...prev, theme: next }));
    } catch (e) {
      // 主题切换失败不影响使用
    }
  };

  const handleUiModeToggle = async () => {
    const next = uiMode === 'classic' ? 'simple' : 'classic';
    setUiMode(next);
    applyWindowSize(next);
    try {
      await SaveSettings({ ...settings, uiMode: next });
      setLocalSettings(prev => ({ ...prev, uiMode: next }));
    } catch (e) {
      // 模式切换失败不影响使用
    }
  };

  // 简易模式：一键切换分流策略（内核与系统代理由程序启动逻辑自动开启，按钮不再启停 TUN）
  //   点击连接 = 绕过大陆（海外走代理、大陆直连）；点击断开 = 全局直连
  const handleSimpleConnect = async () => {
    const next = status.routingMode === 'bypass-cn' ? 'direct' : 'bypass-cn';
    // 连接动作一发出就进入「正在连接…」，检测结果由下面的自动检测更新
    setSimpleNet(next === 'bypass-cn' ? 'checking' : 'idle');
    try {
      await SetRoutingMode(next);
    } catch (e) {
      showToast(String(e?.message || e).replace(/^.*?: /, ''), 'error');
    }
    await refreshStatus();
  };

  const handleLogin = async () => {
    if (loginBusy) return;
    setLoginErr('');
    setLoginBusy(true);
    try {
      const res = await Login(loginForm.email, loginForm.password);
      setAccount(res);
      setLoginForm({ email: '', password: '' });
      setSubscriptions(await GetSubscriptions());
      setNodes(await GetNodes());
      // 登录成功默认进入简洁模式（窗口同步切到紧凑尺寸），并持久化，
      // 否则下次启动 settings 里仍是 classic，窗口不会缩小
      setUiMode('simple');
      applyWindowSize('simple');
      try {
        const s = await GetSettings();
        await SaveSettings({ ...s, uiMode: 'simple' });
        setLocalSettings(prev => ({ ...prev, uiMode: 'simple' }));
      } catch (e) { /* ignore */ }
    } catch (e) {
      setLoginErr(String(e?.message || e).replace(/^.*?: /, ''));
    }
    setLoginBusy(false);
  };

  // 网页授权登录：后端拉起浏览器并在本机等回调，结果通过事件异步回传
  useEffect(() => {
    const offOk = EventsOn('kncloud:web-login', async (acct) => {
      setWebLoginWaiting(false);
      if (acct) setAccount(acct);
      try {
        setSubscriptions(await GetSubscriptions());
        setNodes(await GetNodes());
      } catch (e) { /* ignore */ }
      // 登录成功默认进入简洁模式（窗口同步切到紧凑尺寸），并持久化
      setUiMode('simple');
      applyWindowSize('simple');
      try {
        const s = await GetSettings();
        await SaveSettings({ ...s, uiMode: 'simple' });
        setLocalSettings(prev => ({ ...prev, uiMode: 'simple' }));
      } catch (e) { /* ignore */ }
      showToast('网页登录成功，订阅已同步', 'success');
    });
    const offErr = EventsOn('kncloud:web-login-error', (msg) => {
      setWebLoginWaiting(false);
      showToast(String(msg || '网页登录失败'), 'error');
    });
    return () => {
      if (typeof offOk === 'function') offOk();
      if (typeof offErr === 'function') offErr();
    };
  }, []);

  const handleWebLogin = async () => {
    if (webLoginWaiting) return;
    setLoginErr('');
    try {
      await StartWebLogin();
      setWebLoginWaiting(true);
    } catch (e) {
      setLoginErr(String(e?.message || e).replace(/^.*?: /, ''));
    }
  };

  // 取消等待网页授权：关掉后端的本地回调服务，恢复按钮可用
  const handleCancelWebLogin = async () => {
    try {
      await CancelWebLogin();
    } catch (e) { /* ignore */ }
    setWebLoginWaiting(false);
  };

  const handleLogout = async () => {
    try {
      setAccount(await Logout());
    } catch (e) { /* ignore */ }
  };

  const handleUpdateSubscription = async () => {
    if (syncing) return;
    setSyncing(true);
    try {
      // 1. 刷新官网套餐、流量及最新订阅地址
      const res = await RefreshAccount();
      setAccount(res);
      // 2. 同步最新的服务器节点列表
      await SyncNodes();
      setSubscriptions(await GetSubscriptions());
      setNodes(await GetNodes());
      showToast('订阅及节点已更新', 'success');
    } catch (e) {
      showToast('更新失败：' + (e?.message || e), 'error');
    } finally {
      setSyncing(false);
    }
  };

  const handleSimpleSelectNode = async (id) => {
    if (!id) return;
    try {
      await SelectNode(id);
    } catch (e) {
      showToast('切换节点失败：' + (e?.message || e), 'error');
    }
    await refreshNodesAndStatus();
  };

  // 简易模式节点可用性自动检测：PingNode 在后端起临时内核，经该节点真实请求
  // 探测 URL，结果（delay > 0 可用 / -2 失败）同时回写到节点列表的延迟显示
  const checkSimpleNode = async () => {
    const cur = nodes.find(n => n.active);
    if (!cur) {
      setSimpleNet('fail');
      return;
    }
    setSimpleNet('checking');
    try {
      await PingNode(cur.id);
      const updated = await GetNodes();
      setNodes(updated);
      const n = updated.find(x => x.id === cur.id);
      setSimpleNet(n && n.delay > 0 ? 'ok' : 'fail');
    } catch (e) {
      setSimpleNet('fail');
    }
  };

  // 代理开启时自动检测当前节点；切换节点 / 断开后重新检测或复位
  const simpleActiveId = nodes.find(n => n.active)?.id;
  useEffect(() => {
    if (uiMode !== 'simple') return;
    if ((status.routingMode || 'bypass-cn') === 'direct') {
      setSimpleNet('idle');
      return;
    }
    checkSimpleNode();
  }, [uiMode, status.routingMode, simpleActiveId]);

  // TUN 模式开关（虚拟网卡接管全部流量，与内核代理互斥；后端会自动停/恢复内核与系统代理）
  // SimpleConnect 是同步完成整套启停的（数秒），先立即切换开关并显示进行中文案，完成后落到真实状态
  const handleToggleTun = async (on) => {
    if (tunBusy) return;
    setTunBusy(true);
    setTunPending(on ? 'on' : 'off');
    try {
      await SimpleConnect(on);
    } catch (e) {
      showToast(String(e?.message || e).replace(/^.*?: /, ''), 'error');
    } finally {
      try {
        const s = await GetCoreStatus();
        setStatus(s);
      } catch (_) {}
      setTunPending(null);
      setTunBusy(false);
    }
  };

  const handleRoutingChange = async (mode) => {
    // 切换分流模式会重铺路由、必要时重启内核，失败不提示的话用户只会看到
    // 「点了没反应」。
    try {
      await SetRoutingMode(mode);
    } catch (e) {
      showToast('切换分流模式失败：' + (e?.message || e), 'error');
    }
    // 无论成败都回读一次真实状态，避免界面停留在错误的模式上
    try {
      setStatus(await GetCoreStatus());
    } catch { /* 状态回读失败由下一次轮询兜底 */ }
  };

  const handleSelectNode = async (id) => {
    const target = nodes.find(n => n.id === id);
    if (!target) return;
    // 当前内核连不上的协议（如 Hysteria2）在此拦下，不做乐观更新也不发请求 ——
    // 否则界面会先跳到该节点再被后端打回，选中态与实际状态不一致。
    if (target.unsupported) {
      showToast(target.unsupported, 'error');
      return;
    }
    if (target.active) {
      showToast(`当前已连接至「${target.name}」`, 'info');
      return;
    }
    if (switchingNodeId) return;

    setSwitchingNodeId(id);
    // 乐观更新：立刻让选中圆点与高亮跳到目标节点，界面零延迟即时响应
    setNodes(prev => prev.map(n => ({ ...n, active: n.id === id })));
    setStatus(prev => ({
      ...prev,
      activeNodeName: target.name,
      activeNodeProto: target.protocol
    }));

    try {
      await SelectNode(id);
      showToast(`已切换至「${target.name}」`, 'success');
    } catch (e) {
      showToast('切换节点失败：' + (e?.message || e), 'error');
    } finally {
      const [updatedNodes, updatedStatus] = await Promise.all([
        GetNodes(),
        GetCoreStatus()
      ]);
      if (updatedNodes) setNodes(updatedNodes);
      if (updatedStatus) setStatus(updatedStatus);
      setSwitchingNodeId(null);
    }
  };

  const handlePingSingleNode = async (id, e) => {
    e.stopPropagation();
    try {
      await PingNode(id);
      setNodes(await GetNodes());
    } catch (err) {
      showToast('测速失败：' + (err?.message || err), 'error');
    }
  };

  // Ctrl+R：真连接测速（多选时批量测速所有选中节点；单选或未选时测速当前节点）
  const pingingActiveRef = useRef(false);
  const handlePingSelected = async () => {
    if (pingingActiveRef.current || isPingingAll) return;

    if (selectedNodeIds.length > 1) {
      pingingActiveRef.current = true;
      setIsPingingAll(true);
      setPingProgress({ done: 0, total: selectedNodeIds.length });
      showToast(`正在对选中的 ${selectedNodeIds.length} 个节点进行真连接测速…`, "info");
      try {
        const res = await PingNodes(selectedNodeIds);
        setNodes(res);
        showToast(`已完成 ${selectedNodeIds.length} 个节点的批量测速`, "success");
      } catch (err) {
        showToast("批量测速失败：" + (err?.message || err), "error");
      } finally {
        setIsPingingAll(false);
        setPingProgress({ done: 0, total: 0 });
        pingingActiveRef.current = false;
      }
      return;
    }

    let targetNode = null;
    if (selectedNodeIds.length === 1) {
      targetNode = nodes.find(n => n.id === selectedNodeIds[0]);
    }
    if (!targetNode) {
      targetNode = nodes.find(n => n.active);
    }
    if (!targetNode) {
      showToast("未选择节点，无法测速", "error");
      return;
    }

    pingingActiveRef.current = true;
    showToast(`正在真连接测速「${targetNode.name}」…`);
    try {
      await PingNode(targetNode.id);
      const updated = await GetNodes();
      setNodes(updated);
      const n = updated.find(x => x.id === targetNode.id);
      if (n && n.delay > 0) {
        showToast(`「${n.name}」测速完成：${n.delay} ms`, "success");
      } else {
        showToast(`「${targetNode.name}」测速超时或失败`, "error");
      }
    } catch (e) {
      showToast("测速失败：" + (e?.message || e), "error");
    } finally {
      pingingActiveRef.current = false;
    }
  };

  useEffect(() => {
    const onKey = async (e) => {
      // Ctrl+R 测速
      if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === "r" || e.key === "R")) {
        e.preventDefault(); // 拦截 WebView 默认的 Ctrl+R 刷新页面
        handlePingSelected();
        return;
      }
      // Esc 取消节点多选
      if (e.key === "Escape" && selectedNodeIds.length > 0) {
        e.preventDefault();
        setSelectedNodeIds([]);
        return;
      }
      // Ctrl+A 节点列表全选
      if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === "a" || e.key === "A")) {
        if (activeTab === "servers") {
          const t = e.target;
          const tag = ((t && t.tagName) || "").toLowerCase();
          if (tag !== "input" && tag !== "textarea" && tag !== "select" && !(t && t.isContentEditable)) {
            e.preventDefault();
            const allIds = filteredNodes.map(n => n.id);
            setSelectedNodeIds(allIds);
            showToast(`已全选 ${allIds.length} 个节点（按 Ctrl+R 批量测速）`, "info");
            return;
          }
        }
      }
      if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
      if (activeTab !== "servers") return;
      const key = (e.key || "").toLowerCase();
      if (key !== "c" && key !== "v") return;
      const t = e.target;
      const tag = ((t && t.tagName) || "").toLowerCase();
      if (tag === "input" || tag === "textarea" || tag === "select" || (t && t.isContentEditable)) return;
      e.preventDefault();
      if (key === "c") {
        const cur = nodes.find(n => n.active);
        if (!cur) {
          showToast("未选择节点，无法复制", "error");
          return;
        }
        try {
          await CopyNodeShareLink(cur.id);
          showToast(`已复制「${cur.name}」的分享链接到剪贴板`, "success");
        } catch (err) {
          showToast("复制失败：" + (err?.message || err), "error");
        }
      } else {
        try {
          const count = await ImportNodesFromClipboard();
          if (count > 0) {
            const list = await GetNodes();
            setNodes(list);
            showToast(`已从剪贴板导入 ${count} 个节点`, "success");
            warnUnsupported(list);
          }
        } catch (err) {
          showToast("导入失败：" + (err?.message || err), "error");
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const handlePingAll = async () => {
    if (isPingingAll) return;
    setIsPingingAll(true);
    setPingProgress({ done: 0, total: nodes.length });
    try {
      const res = await PingAllNodes();
      setNodes(res);
    } catch (e) {
      showToast('测速失败：' + (e?.message || e), 'error');
    } finally {
      setIsPingingAll(false);
      setPingProgress({ done: 0, total: 0 });
    }
  };

  const handleDeleteNode = async (id, e) => {
    e.stopPropagation();
    // 删除的若是当前活动节点，后端会重启内核，这一步是可能失败的
    try {
      await DeleteNode(id);
      setNodes(await GetNodes());
    } catch (err) {
      showToast('删除节点失败：' + (err?.message || err), 'error');
    }
  };

  // 批量删除选中节点。后端 DeleteNodes 一次性处理整批（只重启一次内核、
  // 只落盘一次），比循环调用单条删除稳妥。
  const handleDeleteSelected = async () => {
    if (selectedNodeIds.length === 0 || deletingSelected) return;
    const n = selectedNodeIds.length;
    if (!window.confirm(`确定删除选中的 ${n} 个节点吗？此操作不可撤销。`)) return;
    setDeletingSelected(true);
    try {
      await DeleteNodes(selectedNodeIds);
      setSelectedNodeIds([]);
      setNodes(await GetNodes());
      setStatus(await GetCoreStatus());
      showToast(`已删除 ${n} 个节点`, 'success');
    } catch (err) {
      showToast('批量删除失败：' + (err?.message || err), 'error');
    } finally {
      setDeletingSelected(false);
    }
  };

  const resetNodeForm = () => {
    setEditingNodeId(null);
    setNewNode({
      name: '',
      protocol: 'VLESS',
      address: '',
      port: 443,
      uuid: '',
      security: 'reality',
      network: 'tcp',
      group: 'Custom'
    });
  };

  const openEditNode = (node, e) => {
    e.stopPropagation();
    setEditingNodeId(node.id);
    setNewNode({
      name: node.name || '',
      protocol: node.protocol || 'VLESS',
      address: node.address || '',
      port: node.port || 443,
      uuid: node.uuid || '',
      security: node.security || 'none',
      network: node.network || 'tcp',
      group: node.group || 'Custom'
    });
    setShowAddNodeModal(true);
  };

  const handleCreateNode = async () => {
    if (!newNode.address) return;
    try {
      if (editingNodeId) {
        await UpdateNode({ ...newNode, id: editingNodeId, port: parseInt(newNode.port, 10) || 443 });
      } else {
        await AddNode({ ...newNode, port: parseInt(newNode.port, 10) || 443 });
      }
    } catch (e) {
      showToast('保存节点失败：' + (e?.message || e), 'error');
    }
    setShowAddNodeModal(false);
    resetNodeForm();
    await refreshNodesAndStatus();
  };



  const handleImportLinks = async () => {
    if (!importText.trim()) return;
    try {
      const count = await ImportNodesFromLinks(importText);
      setShowImportModal(false);
      setImportText('');
      const list = await GetNodes();
      setNodes(list);
      setActiveTab('servers');
      showToast('成功导入 ' + count + ' 个节点！', 'success');
      warnUnsupported(list);
    } catch (e) {
      showToast('导入失败：' + (e?.message || e), 'error');
    }
  };

  const handleSaveSettings = async () => {
    // 后端会校验端口等输入并可能返回错误；不捕获的话，保存失败也会弹
    // 「保存成功」，用户以为改动生效了。
    try {
      await SaveSettings(settings);
      showToast('首选项已成功保存！', 'success');
    } catch (e) {
      showToast('保存失败：' + (e?.message || e), 'error');
    }
  };

  // Filtered nodes
  const filteredNodes = nodes;

  // 仪表盘推荐节点：按真连接延迟排序（已测速升序 → 超时 → 未测速垫底），取前 3 个。
  // 当前内核连不上的节点（如 Hysteria2）一律排到最后 —— 推荐位不该出现点了就报错的节点。
  const quickPickNodes = [...nodes].sort((a, b) => {
    if (!!a.unsupported !== !!b.unsupported) return a.unsupported ? 1 : -1;
    const rank = (d) => (d > 0 ? 0 : d === -2 ? 1 : 2);
    if (rank(a.delay) !== rank(b.delay)) return rank(a.delay) - rank(b.delay);
    return rank(a.delay) === 0 ? a.delay - b.delay : 0;
  });

  // 登录页与简洁模式同尺寸（420x640）；登录成功后由模式切换逻辑控制窗口
  useEffect(() => {
    if (account && !account.loggedIn) applyWindowSize('simple');
  }, [account && account.loggedIn]);

  // ---------------- 登录页（未登录且未跳过时显示） ----------------
  if (account && !account.loggedIn) {
    return <LoginView handleCancelWebLogin={handleCancelWebLogin} handleLogin={handleLogin} handleWebLogin={handleWebLogin} loginBusy={loginBusy} loginErr={loginErr} loginForm={loginForm} loginLogo={loginLogo} renderToasts={renderToasts} setLoginForm={setLoginForm} theme={theme} webLoginWaiting={webLoginWaiting} />;
  }

  // ---------------- 简易模式（点击即用，无复杂设置） ----------------
  if (uiMode === 'simple') {
    return <SimpleView account={account} brandLogo={brandLogo} closeWindowTitle={closeWindowTitle} delayColor={delayColor} delayText={delayText} fmtGB={fmtGB} handleLogout={handleLogout} handlePingAll={handlePingAll} handleSimpleConnect={handleSimpleConnect} handleSimpleSelectNode={handleSimpleSelectNode} handleThemeToggle={handleThemeToggle} handleUiModeToggle={handleUiModeToggle} handleUpdateSubscription={handleUpdateSubscription} isPingingAll={isPingingAll} nodeMenuOpen={nodeMenuOpen} nodes={nodes} renderToasts={renderToasts} setNodeMenuOpen={setNodeMenuOpen} simpleNet={simpleNet} status={status} syncing={syncing} theme={theme} />;
  }

  return (
    <div className={`app-window ${theme === 'dark' ? 'dark-theme' : ''}`}>
      {renderToasts()}
      {/* Windows 11 TitleBar */}
      <header className="titlebar drag-region">
        <div className="titlebar-left">
          <img src={brandLogo} alt="KNcloud-WIN" style={{ height: "22px", width: "auto", display: "block" }} />
        </div>



        <div className="titlebar-right no-drag">
          <button className="theme-toggle-btn" onClick={handleUiModeToggle} title="切换到简易模式（点击即用）">
            <LayoutGrid size={15} />
          </button>
          <button
            className="theme-toggle-btn"
            onClick={handleThemeToggle}
            title="切换浅色 / 深色主题"
          >
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

      {/* Main Body */}
      <div className="app-body">
        {/* Left Windows 11 Navigation Bar */}
        <nav className={`nav-sidebar ${sidebarCollapsed ? 'collapsed' : ''}`}>
          <div className="nav-top-actions">
            <button
              className="hamburger-btn"
              onClick={() => setSidebarCollapsed(!sidebarCollapsed)}
              title="展开 / 折叠侧边栏"
            >
              <Menu size={16} />
            </button>

            <div className="nav-items-list">
              <button
                className={`nav-item-btn ${activeTab === 'dashboard' ? 'active' : ''}`}
                onClick={() => setActiveTab('dashboard')}
              >
                <Activity size={17} />
                {!sidebarCollapsed && <span>仪表盘</span>}
              </button>
              <button
                className={`nav-item-btn ${activeTab === 'servers' ? 'active' : ''}`}
                onClick={() => setActiveTab('servers')}
              >
                <Server size={17} />
                {!sidebarCollapsed && <span>节点列表</span>}
              </button>
              <button
                className={`nav-item-btn ${activeTab === 'routing' ? 'active' : ''}`}
                onClick={() => setActiveTab('routing')}
              >
                <GitFork size={17} />
                {!sidebarCollapsed && <span>路由分流</span>}
              </button>

              <button
                className={`nav-item-btn ${activeTab === 'logs' ? 'active' : ''}`}
                onClick={() => setActiveTab('logs')}
              >
                <Terminal size={17} />
                {!sidebarCollapsed && <span>实时日志</span>}
              </button>
            </div>
          </div>

          <div className="nav-footer-area">
            <button
              className={`nav-item-btn ${activeTab === 'settings' ? 'active' : ''}`}
              onClick={() => setActiveTab('settings')}
            >
              <Settings size={17} />
              {!sidebarCollapsed && <span>首选项设置</span>}
            </button>
          </div>
        </nav>

        {/* Right Main Content */}
        <main className="content-surface">
          {/* TAB 1: DASHBOARD */}
          {activeTab === 'dashboard' && (
            <DashboardTab account={account} fmtGB={fmtGB} handleLogout={handleLogout} handleRoutingChange={handleRoutingChange} handleSelectNode={handleSelectNode} handleToggleTun={handleToggleTun} handleUpdateSubscription={handleUpdateSubscription} nodes={nodes} quickPickNodes={quickPickNodes} setActiveTab={setActiveTab} settings={settings} status={status} subscriptions={subscriptions} syncing={syncing} tunBusy={tunBusy} tunPending={tunPending} />
          )}

          {/* TAB 2: SERVERS (NODES) */}
          {activeTab === 'servers' && (
            <ServersTab deletingSelected={deletingSelected} filteredNodes={filteredNodes} handleDeleteNode={handleDeleteNode} handleDeleteSelected={handleDeleteSelected} handlePingAll={handlePingAll} handlePingSelected={handlePingSelected} handlePingSingleNode={handlePingSingleNode} handleSelectNode={handleSelectNode} isPingingAll={isPingingAll} pingProgress={pingProgress} openEditNode={openEditNode} selectedNodeIds={selectedNodeIds} setSelectedNodeIds={setSelectedNodeIds} setShowAddNodeModal={setShowAddNodeModal} showToast={showToast} switchingNodeId={switchingNodeId} />
          )}

          {/* TAB 3: ROUTING */}
          {activeTab === 'routing' && (
            <RoutingTab handleRoutingChange={handleRoutingChange} status={status} />
          )}

          {/* TAB 5: LOGS */}
          {activeTab === 'logs' && (
            <LogsTab logs={logs} refreshAllData={refreshAllData} />
          )}

          {/* TAB 6: SETTINGS */}
          {activeTab === 'settings' && (
            <SettingsTab handleSaveSettings={handleSaveSettings} setLocalSettings={setLocalSettings} settings={settings} />
          )}
        </main>
      </div>

      {/* Modal: Add Node Dialog */}
      {showAddNodeModal && (
        <div className="modal-overlay" onClick={() => { setShowAddNodeModal(false); resetNodeForm(); }}>
          <div className="win11-dialog" onClick={e => e.stopPropagation()}>
            <h2 style={{ fontSize: '18px', fontWeight: 600, color: 'var(--text-primary)' }}>{editingNodeId ? '编辑节点' : '添加节点'}</h2>
            
            <div className="form-group">
              <label className="form-label">节点名称 (备注)</label>
              <input
                type="text"
                className="win11-input"
                placeholder="例如: 日本东京 01"
                value={newNode.name}
                onChange={e => setNewNode({ ...newNode, name: e.target.value })}
              />
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '12px' }}>
              <div className="form-group">
                <label className="form-label">协议类型</label>
                <select
                  className="win11-input"
                  value={newNode.protocol}
                  onChange={e => setNewNode({ ...newNode, protocol: e.target.value })}
                >
                  <option value="VLESS">VLESS (推荐)</option>
                  <option value="VMess">VMess</option>
                  <option value="Trojan">Trojan</option>
                  <option value="Hysteria2">Hysteria2</option>
                  <option value="Shadowsocks">Shadowsocks</option>
                </select>
              </div>

              <div className="form-group">
                <label className="form-label">传输协议 (Network)</label>
                <select
                  className="win11-input"
                  value={newNode.network}
                  onChange={e => setNewNode({ ...newNode, network: e.target.value })}
                >
                  <option value="tcp">TCP</option>
                  <option value="ws">WebSocket (WS)</option>
                  <option value="grpc">gRPC</option>
                  <option value="udp">UDP</option>
                </select>
              </div>
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: '12px' }}>
              <div className="form-group">
                <label className="form-label">服务器地址 (Domain / IP)</label>
                <input
                  type="text"
                  className="win11-input"
                  placeholder="hk.example.com"
                  value={newNode.address}
                  onChange={e => setNewNode({ ...newNode, address: e.target.value })}
                />
              </div>

              <div className="form-group">
                <label className="form-label">端口 (Port)</label>
                <input
                  type="number"
                  className="win11-input"
                  value={newNode.port}
                  onChange={e => setNewNode({ ...newNode, port: e.target.value })}
                />
              </div>
            </div>

            <div className="form-group">
              <label className="form-label">用户 ID / 密码 (UUID / Password)</label>
              <input
                type="text"
                className="win11-input"
                placeholder="UUID 字符串或连接密钥"
                value={newNode.uuid}
                onChange={e => setNewNode({ ...newNode, uuid: e.target.value })}
              />
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '10px', marginTop: '10px' }}>
              <button className="win11-btn" onClick={() => { setShowAddNodeModal(false); resetNodeForm(); }}>取消</button>
              <button className="win11-btn primary" onClick={handleCreateNode}>{editingNodeId ? '保存修改' : '添加并保存'}</button>
            </div>
          </div>
        </div>
      )}

      {/* Modal: Import Share Links */}
      {showImportModal && (
        <div className="modal-overlay" onClick={() => setShowImportModal(false)}>
          <div className="win11-dialog" onClick={e => e.stopPropagation()}>
            <h2 style={{ fontSize: '18px', fontWeight: 600, color: 'var(--text-primary)' }}>批量导入分享链接</h2>
            <p style={{ fontSize: '12px', color: 'var(--text-secondary)', margin: '4px 0 10px' }}>
              每行一条，支持 vmess:// vless:// trojan:// ss:// hysteria2:// 链接，或直接粘贴 Base64 订阅内容。
            </p>
            <div className="form-group">
              <textarea
                className="win11-input"
                rows={8}
                placeholder={'vless://uuid@host:443?security=reality&type=grpc#节点名\nvmess://eyJ2IjoiMiIsLi4u\nss://YWVzLTI1Ni1nY206cGFzcw==@host:8388#SS节点'}
                value={importText}
                onChange={e => setImportText(e.target.value)}
                style={{ fontFamily: 'Consolas, monospace', fontSize: '12px', resize: 'vertical' }}
              />
            </div>
            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '10px', marginTop: '10px' }}>
              <button className="win11-btn" onClick={() => setShowImportModal(false)}>取消</button>
              <button className="win11-btn primary" onClick={handleImportLinks}>解析并导入</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
