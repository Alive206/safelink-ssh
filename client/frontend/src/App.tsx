import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import {
  AddTunnel,
  ClearLogs,
  CloseSSHSession,
  CreateSSHSession,
  DeleteSSHConnection,
  DeleteSubscription,
  DeleteTunnel,
  GetDataDir,
  GetClientSettings,
  GetLogs,
  GetVersion,
  ImportSubscription,
  ListProxyNodes,
  ListSSHConnections,
  ListSubscriptions,
  ListTunnels,
  ProxyStatus,
  RefreshAllSubscriptions,
  RefreshSubscription,
  ResizeSSHSession,
  SaveSSHConnection,
  SendSSHInput,
  SetAutoStartEnabled,
  SetProxyMode,
  SetSystemProxyEnabled,
  StartProxyNode,
  StartTunnel,
  StopProxy,
  StopTunnel,
  TestProxyNode,
  ToggleSubscription,
  UpdateSubscription,
  UpdateTunnel,
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'
import type { config, main, manager, proxycore, proxysubscription, store } from '../wailsjs/go/models'

type NavKey = 'home' | 'nodes' | 'subscriptions' | 'tunnels' | 'terminal' | 'settings' | 'logs'

type DashboardNode = {
  id: string
  name: string
  country: string
  city: string
  flagCode: string
  protocol: string
  server: string
  port: number
  method: string
  password: string
  security: string
  udp: boolean
  latency: number
  latencyState: 'untested' | 'testing' | 'ok' | 'failed'
  latencyError: string
  speed: number
  source?: proxysubscription.ProxyNode
}

type TunnelRow = {
  id: string
  name: string
  type: string
  listen: string
  forward: string
  detail: string
  endpoint: string
  running: boolean
  updatedAt: string
  source?: manager.Status
}

type SSHRow = {
  id: string
  name: string
  detail: string
  last: string
  source?: store.SSHConnection
}

const NODE_TEST_CONCURRENCY = 6

type ModalKind = 'subscription' | 'tunnel' | 'ssh' | null

type ProxyMode = 'rule' | 'global' | 'direct'

type SubscriptionForm = {
  id: string
  name: string
  url: string
  autoRefresh: boolean
  intervalMin: number
}

type TunnelForm = {
  editingName: string
  name: string
  mode: 'local' | 'remote' | 'dynamic'
  listen: string
  forward: string
  sshAddr: string
  sshUser: string
  password: string
}

type SSHForm = {
  id: string
  name: string
  addr: string
  user: string
  password: string
}

type SSHOutputEvent = {
  session_id: string
  data: string
}

type SSHClosedEvent = {
  session_id: string
  message?: string
}

const navItems: Array<{ key: NavKey; label: string; icon: string }> = [
  { key: 'home', label: '首页', icon: 'home' },
  { key: 'nodes', label: '节点', icon: 'node' },
  { key: 'subscriptions', label: '订阅', icon: 'sub' },
  { key: 'tunnels', label: 'SSH 隧道', icon: 'tunnel' },
  { key: 'terminal', label: 'SSH 终端', icon: 'term' },
  { key: 'settings', label: '设置', icon: 'gear' },
  { key: 'logs', label: '日志', icon: 'log' },
]

const logoSrc = new URL('./static/logo.png', import.meta.url).href

const settingsTabs = ['常规设置', '代理设置', '连接设置', '订阅设置', 'SSH 设置', '外观设置', '高级设置']

const defaultClientSettings: store.ClientSettings = {
  proxy_mode: 'rule',
  system_proxy: false,
  auto_start: false,
  bypass_lan: false,
  auto_connect: false,
  minimize_to_tray: false,
}

const proxyModeOptions: Array<{ value: ProxyMode; label: string }> = [
  { value: 'rule', label: '规则模式' },
  { value: 'global', label: '全局模式' },
  { value: 'direct', label: '直连模式' },
]

const emptySubscriptionForm: SubscriptionForm = {
  id: '',
  name: '',
  url: '',
  autoRefresh: true,
  intervalMin: 360,
}

const emptyTunnelForm: TunnelForm = {
  editingName: '',
  name: '',
  mode: 'local',
  listen: '127.0.0.1:1080',
  forward: '',
  sshAddr: '',
  sshUser: '',
  password: '',
}

const emptySSHForm: SSHForm = {
  id: '',
  name: '',
  addr: '',
  user: '',
  password: '',
}

function App() {
  const [activeNav, setActiveNav] = useState<NavKey>('home')
  const [version, setVersion] = useState('1.0.0')
  const [dataDir, setDataDir] = useState('')
  const [tunnels, setTunnels] = useState<manager.Status[]>([])
  const [subscriptions, setSubscriptions] = useState<store.SubscriptionSource[]>([])
  const [proxyNodes, setProxyNodes] = useState<proxysubscription.ProxyNode[]>([])
  const [proxyStatus, setProxyStatus] = useState<proxycore.Status | null>(null)
  const [clientSettings, setClientSettings] = useState<store.ClientSettings>(defaultClientSettings)
  const [sshConnections, setSSHConnections] = useState<store.SSHConnection[]>([])
  const [logs, setLogs] = useState<main.LogEntry[]>([])
  const [proxyTestResults, setProxyTestResults] = useState<Record<string, proxycore.TestResult>>({})
  const [testingNodeNames, setTestingNodeNames] = useState<Set<string>>(() => new Set())
  const [selectedNodeID, setSelectedNodeID] = useState('')
  const [selectedTunnelID, setSelectedTunnelID] = useState('')
  const [selectedSSHID, setSelectedSSHID] = useState('')
  const [modal, setModal] = useState<ModalKind>(null)
  const [subscriptionForm, setSubscriptionForm] = useState<SubscriptionForm>(emptySubscriptionForm)
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(emptyTunnelForm)
  const [sshForm, setSSHForm] = useState<SSHForm>(emptySSHForm)
  const [terminalSessionID, setTerminalSessionID] = useState('')
  const [terminalTitle, setTerminalTitle] = useState('')
  const [terminalHost, setTerminalHost] = useState('')
  const [terminalConnected, setTerminalConnected] = useState(false)
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  const backendReady = isBackendReady()

  const refresh = useCallback(async () => {
    if (!isBackendReady()) {
      return
    }

    try {
      const [nextVersion, nextTunnels, nextSubscriptions, nextProxyNodes, nextProxyStatus, nextSettings, nextSSHConnections, nextDataDir, nextLogs] = await Promise.all([
        GetVersion(),
        ListTunnels(),
        ListSubscriptions(),
        ListProxyNodes(),
        ProxyStatus(),
        GetClientSettings(),
        ListSSHConnections(),
        GetDataDir(),
        GetLogs(),
      ])
      setVersion(nextVersion || '1.0.0')
      setTunnels(nextTunnels ?? [])
      setSubscriptions(nextSubscriptions ?? [])
      setProxyNodes(nextProxyNodes ?? [])
      setProxyStatus(nextProxyStatus ?? null)
      setClientSettings({ ...defaultClientSettings, ...(nextSettings ?? {}) })
      setSSHConnections(nextSSHConnections ?? [])
      setDataDir(nextDataDir || '')
      setLogs(nextLogs ?? [])
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    refresh()
    const timer = window.setInterval(refresh, 3000)
    return () => window.clearInterval(timer)
  }, [refresh])

  useEffect(() => {
    if (!backendReady || !terminalSessionID) {
      return
    }
    const offClosed = EventsOn('ssh:closed', (event: SSHClosedEvent) => {
      if (event?.session_id === terminalSessionID) {
        setTerminalConnected(false)
      }
    })
    return () => offClosed()
  }, [backendReady, terminalSessionID])

  const nodes = useMemo(() => {
    return proxyNodes.map((node, index) => toDashboardNode(node, index, proxyTestResults[node.name], testingNodeNames.has(node.name)))
  }, [proxyNodes, proxyTestResults, testingNodeNames])

  const sshTunnels = useMemo(() => tunnels.filter((item) => normalizeMode(item.config.mode) !== 'vpn'), [tunnels])
  const tunnelRows = useMemo(() => sshTunnels.map(toTunnelRow), [sshTunnels])
  const sshRows = useMemo(() => sshConnections.map(toSSHRow), [sshConnections])

  useEffect(() => {
    if (nodes.length === 0) {
      setSelectedNodeID('')
      return
    }
    const runningID = nodes.find((node) => node.name === proxyStatus?.node_name)?.id
    if (runningID && !selectedNodeID) {
      setSelectedNodeID(runningID)
      return
    }
    if (!selectedNodeID || !nodes.some((node) => node.id === selectedNodeID)) {
      setSelectedNodeID(nodes[0].id)
    }
  }, [nodes, proxyStatus?.node_name, selectedNodeID])

  useEffect(() => {
    if (tunnelRows.length === 0) {
      setSelectedTunnelID('')
      return
    }
    if (!selectedTunnelID || !tunnelRows.some((row) => row.id === selectedTunnelID)) {
      setSelectedTunnelID(tunnelRows[0].id)
    }
  }, [selectedTunnelID, tunnelRows])

  useEffect(() => {
    if (sshRows.length === 0) {
      setSelectedSSHID('')
      return
    }
    if (!selectedSSHID || !sshRows.some((row) => row.id === selectedSSHID)) {
      setSelectedSSHID(sshRows[0].id)
    }
  }, [selectedSSHID, sshRows])

  const selectedNode = nodes.find((node) => node.id === selectedNodeID) ?? nodes[0]
  const selectedTunnel = tunnelRows.find((row) => row.id === selectedTunnelID) ?? tunnelRows[0]
  const selectedSSH = sshRows.find((row) => row.id === selectedSSHID) ?? sshRows[0]
  const connected = proxyStatus?.state === 'running'
  const proxyMode = normalizeProxyMode(clientSettings.proxy_mode || proxyStatus?.mode || 'rule')
  const runningNode = nodes.find((node) => node.name === proxyStatus?.node_name)
  const currentNode = connected ? runningNode : undefined
  const homePreviewNodes = useMemo(() => topLatencyNodes(nodes), [nodes])
  const tunnelUploadBytes = tunnels.reduce((sum, item) => sum + (item.stats?.bytes_out ?? 0), 0)
  const tunnelDownloadBytes = tunnels.reduce((sum, item) => sum + (item.stats?.bytes_in ?? 0), 0)
  const uploadTotalBytes = proxyStatus?.upload_total_bytes ?? tunnelUploadBytes
  const downloadTotalBytes = proxyStatus?.download_total_bytes ?? tunnelDownloadBytes
  const uploadSpeedBps = proxyStatus?.upload_speed_bps ?? 0
  const downloadSpeedBps = proxyStatus?.download_speed_bps ?? 0
  const connectedSince = proxyStatus?.started_at ? formatClockDuration(proxyStatus.started_at) : '00:00:00'
  const terminalLabel = terminalTitle ? formatSSHConnectionLabel(terminalTitle, terminalHost) : selectedSSH ? sshRowLabel(selectedSSH) : '未选择连接'

  async function runAction(label: string, action: () => Promise<unknown>) {
    if (!backendReady) {
      setNotice('当前为前端预览模式')
      window.setTimeout(() => setNotice(''), 1800)
      return
    }

    setBusy(label)
    setNotice('')
    setError('')
    try {
      await action()
      await refresh()
      setNotice(`${label}成功`)
      window.setTimeout(() => setNotice(''), 1800)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy('')
    }
  }

  async function connectNode(node = selectedNode) {
    const source = node?.source
    if (!source) {
      setNotice('暂无可连接节点')
      window.setTimeout(() => setNotice(''), 1800)
      return
    }
    setSelectedNodeID(node.id)
    const alreadyActive = connected && proxyStatus?.node_name === node.name && clientSettings.system_proxy
    if (alreadyActive || busy) {
      return
    }
    await runAction(connected ? '切换节点' : '连接节点', () => StartProxyNode(source.name))
  }

  async function testAllNodes() {
    const testableNodes = nodes.filter((node): node is DashboardNode & { source: proxysubscription.ProxyNode } => Boolean(node.source))
    if (testableNodes.length === 0) {
      setNotice('暂无可测试节点')
      window.setTimeout(() => setNotice(''), 1800)
      return
    }
    if (!backendReady) {
      setNotice('当前为前端预览模式')
      window.setTimeout(() => setNotice(''), 1800)
      return
    }

    const total = testableNodes.length
    const testNames = new Set(testableNodes.map((node) => node.source.name))
    setBusy(`测试节点 0/${total}`)
    setNotice(`节点测试中：0/${total}`)
    setError('')
    setTestingNodeNames(testNames)
    setProxyTestResults((current) => {
      const next = { ...current }
      testNames.forEach((name) => {
        delete next[name]
      })
      return next
    })
    let okCount = 0
    let completedCount = 0
    try {
      const runNodeTest = async (node: DashboardNode & { source: proxysubscription.ProxyNode }) => {
        let result: proxycore.TestResult
        try {
          result = await TestProxyNode(node.source.name)
        } catch (err) {
          result = {
            node_name: node.source.name,
            ok: false,
            latency_ms: 0,
            error: errorMessage(err),
            tested_at: new Date().toISOString(),
          } as proxycore.TestResult
        }
        if (result.ok) {
          okCount += 1
        }
        completedCount += 1
        setProxyTestResults((current) => ({ ...current, [result.node_name]: result }))
        setTestingNodeNames((current) => {
          const next = new Set(current)
          next.delete(result.node_name)
          return next
        })
        setBusy(`测试节点 ${completedCount}/${total}`)
        setNotice(`节点测试中：${completedCount}/${total}，${okCount} 可用`)
      }

      const workerCount = Math.min(NODE_TEST_CONCURRENCY, testableNodes.length)
      const workers = Array.from({ length: workerCount }, async (_, workerIndex) => {
        for (let index = workerIndex; index < testableNodes.length; index += workerCount) {
          await runNodeTest(testableNodes[index])
        }
      })
      await Promise.all(workers)
      setTestingNodeNames(new Set())
      await refresh()
      setNotice(`节点测试完成：${okCount}/${total} 可用`)
      window.setTimeout(() => setNotice(''), 2200)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setTestingNodeNames(new Set())
      setBusy('')
    }
  }

  async function changeProxyMode(mode: ProxyMode) {
    if (clientSettings.proxy_mode === mode || busy) {
      return
    }
    await runAction('切换代理模式', () => SetProxyMode(mode))
  }

  async function toggleSystemProxy() {
    if (busy) {
      return
    }
    if (clientSettings.system_proxy || connected) {
      await runAction('关闭系统代理', () => (connected ? StopProxy() : SetSystemProxyEnabled(false)))
      return
    }
    await connectNode(selectedNode)
  }

  async function toggleAutoStart() {
    if (busy) {
      return
    }
    await runAction(clientSettings.auto_start ? '关闭开机自启' : '开启开机自启', () => SetAutoStartEnabled(!clientSettings.auto_start))
  }

  async function toggleTunnel(row: TunnelRow) {
    if (!row.source) {
      return
    }
    await runAction(row.running ? '停止隧道' : '启动隧道', () => (row.running ? StopTunnel(row.source!.config.name) : StartTunnel(row.source!.config.name)))
  }

  function openSubscriptionForm(item?: store.SubscriptionSource) {
    setSubscriptionForm(
      item
        ? {
            id: item.id,
            name: item.name,
            url: item.url,
            autoRefresh: item.auto_refresh,
            intervalMin: item.interval_min || 360,
          }
        : emptySubscriptionForm,
    )
    setModal('subscription')
  }

  async function submitSubscription(event: FormEvent) {
    event.preventDefault()
    const form = subscriptionForm
    await runAction(form.id ? '更新订阅' : '添加订阅', async () => {
      if (form.id) {
        await UpdateSubscription(form.id, form.name.trim(), form.url.trim(), form.autoRefresh, form.intervalMin)
      } else {
        await ImportSubscription(form.name.trim(), form.url.trim())
      }
      setModal(null)
      setSubscriptionForm(emptySubscriptionForm)
    })
  }

  async function refreshSub(item: store.SubscriptionSource) {
    await runAction('刷新订阅', () => RefreshSubscription(item.id))
  }

  async function refreshAllSubs() {
    await runAction('刷新全部订阅', () => RefreshAllSubscriptions())
  }

  async function toggleSub(item: store.SubscriptionSource) {
    await runAction(item.enabled ? '停用订阅' : '启用订阅', () => ToggleSubscription(item.id, !item.enabled))
  }

  async function deleteSub(item: store.SubscriptionSource) {
    if (!window.confirm(`确定删除订阅 "${item.name}" 吗？`)) {
      return
    }
    await runAction('删除订阅', () => DeleteSubscription(item.id))
  }

  function openTunnelForm(row?: TunnelRow) {
    const cfg = row?.source?.config
    const mode = normalizeMode(cfg?.mode || 'local')
    setTunnelForm(
      cfg
        ? {
            editingName: cfg.name,
            name: cfg.name,
            mode: mode === 'vpn' ? 'local' : mode,
            listen: cfg.listen || '',
            forward: cfg.forward || '',
            sshAddr: cfg.ssh?.addr || '',
            sshUser: cfg.ssh?.user || '',
            password: cfg.ssh?.password || '',
          }
        : emptyTunnelForm,
    )
    setModal('tunnel')
  }

  async function submitTunnel(event: FormEvent) {
    event.preventDefault()
    const cfg = buildTunnelConfig(tunnelForm)
    await runAction(tunnelForm.editingName ? '更新隧道' : '新增隧道', async () => {
      if (tunnelForm.editingName) {
        await UpdateTunnel(tunnelForm.editingName, cfg)
      } else {
        await AddTunnel(cfg)
      }
      setModal(null)
      setTunnelForm(emptyTunnelForm)
    })
  }

  async function deleteSelectedTunnel() {
    if (!selectedTunnel) {
      return
    }
    if (!window.confirm(`确定删除隧道 "${selectedTunnel.name}" 吗？`)) {
      return
    }
    await runAction('删除隧道', () => DeleteTunnel(selectedTunnel.name))
  }

  function openSSHForm(row?: SSHRow) {
    const conn = row?.source
    setSSHForm(
      conn
        ? {
            id: conn.id,
            name: conn.name,
            addr: conn.addr,
            user: conn.user,
            password: conn.password,
          }
        : emptySSHForm,
    )
    setModal('ssh')
  }

  async function submitSSHConnection(event: FormEvent) {
    event.preventDefault()
    const form = sshForm
    await runAction(form.id ? '更新 SSH 连接' : '新增 SSH 连接', async () => {
      await SaveSSHConnection({
        id: form.id,
        name: form.name.trim(),
        addr: form.addr.trim(),
        user: form.user.trim(),
        password: form.password,
      })
      setModal(null)
      setSSHForm(emptySSHForm)
    })
  }

  async function deleteSelectedSSH() {
    if (!selectedSSH?.source) {
      return
    }
    if (!window.confirm(`确定删除 SSH 连接 "${selectedSSH.name}" 吗？`)) {
      return
    }
    await runAction('删除 SSH 连接', () => DeleteSSHConnection(selectedSSH.source!.id))
  }

  async function openTerminal(row = selectedSSH) {
    const conn = row?.source
    if (!conn) {
      setNotice('暂无可连接的 SSH 配置')
      window.setTimeout(() => setNotice(''), 1800)
      return
    }
    await runAction('连接 SSH 终端', async () => {
      if (terminalSessionID) {
        await CloseSSHSession(terminalSessionID).catch(() => undefined)
        setTerminalConnected(false)
      }
      const sessionID = await CreateSSHSession({
        addr: conn.addr,
        user: conn.user,
        identity_file: '',
        passphrase: '',
        password: conn.password,
        rows: 24,
        cols: 80,
      })
      setTerminalSessionID(sessionID)
      setTerminalTitle(conn.name || `${conn.user}@${hostFromAddr(conn.addr)}`)
      setTerminalHost(hostFromAddr(conn.addr))
      setTerminalConnected(true)
      setActiveNav('terminal')
    })
  }

  async function closeTerminal() {
    if (terminalSessionID) {
      await CloseSSHSession(terminalSessionID).catch(() => undefined)
    }
    setTerminalSessionID('')
    setTerminalTitle('')
    setTerminalHost('')
    setTerminalConnected(false)
  }

  async function clearLogEntries() {
    await runAction('清空日志', async () => {
      await ClearLogs()
      setLogs([])
    })
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <BrandLogo size="large" />
          <span>SafeLink</span>
        </div>

        <nav className="nav-list">
          {navItems.map((item, index) => (
            <button
              className={`nav-item ${activeNav === item.key ? 'active' : ''} ${index === 5 ? 'with-divider' : ''}`}
              key={item.key}
              onClick={() => setActiveNav(item.key)}
              type="button"
            >
              <Icon name={item.icon} />
              <span>{item.label}</span>
            </button>
          ))}
        </nav>

        <div className="sidebar-status">
          <div className="status-line">
            <span className={`dot ${connected ? 'green' : 'gray'}`} />
            <strong>{connected ? '已连接' : '未连接'}</strong>
          </div>
          <p>当前模式： 规则模式</p>
          <p>本地端口： {proxyStatus?.socks_addr ? portFromAddr(proxyStatus.socks_addr) : '-'}</p>
        </div>
        <span className="version">v{version}</span>
      </aside>

      <main className="workspace">
        {(notice || error) && <div className={`toast ${error ? 'error' : ''}`}>{error || notice}</div>}
        {activeNav === 'home' && renderHomePage()}
        {activeNav === 'nodes' && renderNodesPage()}
        {activeNav === 'subscriptions' && renderSubscriptionsPage()}
        {activeNav === 'tunnels' && renderTunnelsPage()}
        {activeNav === 'terminal' && renderTerminalPage()}
        {activeNav === 'settings' && renderSettingsPage()}
        {activeNav === 'logs' && renderLogsPage()}
      </main>
      {renderModal()}
    </div>
  )

  function renderHomePage() {
    return (
      <div className="home-page">
        <section className="top-grid">
          <div className="card hero-card">
            <div className="hero-content">
              <BrandLogo size="xl" />
              <div>
                <div className="connection-state">{connected ? '已连接' : '未连接'}</div>
                <p className="muted">当前节点</p>
                <div className="current-node">
                  {currentNode ? <Flag code={currentNode.flagCode} /> : <span className="flag-placeholder" />}
                  <span>{currentNode?.name ?? '暂无连接节点'}</span>
                </div>
                <span className="protocol-pill">{currentNode?.protocol ?? '-'}</span>
              </div>
            </div>
            <div className="map-watermark" aria-hidden="true"><span /></div>
          </div>

          <div className="top-stack">
            <div className="card stat-card">
              <MetricBlock label="上传" icon="up" value={formatByteRate(uploadSpeedBps)} title={`累计上传 ${formatBytes(uploadTotalBytes)}`} />
              <MetricBlock label="下载" icon="down" value={formatByteRate(downloadSpeedBps)} title={`累计下载 ${formatBytes(downloadTotalBytes)}`} />
              <MetricBlock label="延迟" icon="down" value={formatLatency(currentNode)} />
              <MetricBlock label="连接时长" icon="clock" value={connectedSince} />
            </div>

            <div className="mode-row">
              <div className="card mode-card">
                <p className="section-label">代理模式</p>
                <div className="segment">
                  {proxyModeOptions.map((item) => (
                    <button className={proxyMode === item.value ? 'selected' : ''} disabled={Boolean(busy)} key={item.value} onClick={() => changeProxyMode(item.value)} type="button">
                      {item.label}
                    </button>
                  ))}
                </div>
              </div>
              <div className="card switch-card">
                <ToggleRow checked={clientSettings.system_proxy} disabled={Boolean(busy) || (!connected && !selectedNode)} label="系统代理" onToggle={toggleSystemProxy} title={clientSettings.system_proxy ? '关闭系统代理并断开连接' : '开启系统代理并连接当前节点'} />
                <ToggleRow checked={clientSettings.auto_start} disabled={Boolean(busy)} label="开机自启" onToggle={toggleAutoStart} />
              </div>
            </div>
          </div>
        </section>

        <section className="card home-node-section">
          <PanelTitle title="节点概览" action={<button className="small-outline" onClick={() => setActiveNav('nodes')} type="button">查看全部节点</button>} />
          {renderHomeNodeCards()}
        </section>

        <section className="bottom-grid">
          {renderTunnelCompactCard()}
          {renderTerminalCompactCard()}
        </section>
      </div>
    )
  }

  function renderHomeNodeCards() {
    const previewNodes = homePreviewNodes

    if (previewNodes.length === 0) {
      return <EmptyList text={backendReady ? '暂无节点，请先启用订阅' : '未连接后端，暂无节点数据'} />
    }

    return (
      <div className="home-node-grid">
        {previewNodes.map((node) => {
          const selected = selectedNode?.id === node.id
          const running = proxyStatus?.node_name === node.name && connected
          return (
            <button
              className={`home-node-card ${selected ? 'selected' : ''}`}
              key={node.id}
              onClick={() => connectNode(node)}
              type="button"
            >
              <span className="home-node-selected" aria-hidden="true" />
              <span className="home-node-top">
                <span className="home-node-name"><Flag code={node.flagCode} /><strong title={node.name}>{node.name}</strong></span>
                <span className={running ? 'mini-state running' : 'mini-state'}><span className={`dot ${running ? 'green' : 'gray'}`} />{running ? '使用中' : '点击连接'}</span>
              </span>
              <span className="home-node-meta">
                <span className="type-pill">{node.protocol}</span>
                <span>{node.country} · {node.city}</span>
              </span>
              <span className="home-node-address" title={`${node.server}:${node.port}`}>{node.server}:{node.port}</span>
              <span className="home-node-bottom">
                <span className={`${nodeStatusClass(node)} node-card-status`}><span className={`dot ${nodeStatusDot(node)}`} />{nodeStatusText(node)}</span>
                <strong className={latencyClass(node)} title={node.latencyError || undefined}>{formatLatency(node)}</strong>
              </span>
            </button>
          )
        })}
      </div>
    )
  }

  function renderModal() {
    if (!modal) {
      return null
    }
    return (
      <div className="modal-backdrop" onMouseDown={() => setModal(null)}>
        <div className="modal-card" onMouseDown={(event) => event.stopPropagation()}>
          {modal === 'subscription' && (
            <form onSubmit={submitSubscription}>
              <ModalHeader title={subscriptionForm.id ? '编辑订阅' : '添加订阅'} onClose={() => setModal(null)} />
              <FormField label="订阅名称" value={subscriptionForm.name} onChange={(value) => setSubscriptionForm((current) => ({ ...current, name: value }))} required />
              <FormField label="订阅地址" value={subscriptionForm.url} onChange={(value) => setSubscriptionForm((current) => ({ ...current, url: value }))} required />
              <div className="form-grid">
                <label className="form-check"><input checked={subscriptionForm.autoRefresh} onChange={(event) => setSubscriptionForm((current) => ({ ...current, autoRefresh: event.target.checked }))} type="checkbox" />自动刷新</label>
                <FormField label="刷新间隔（分钟）" type="number" value={String(subscriptionForm.intervalMin)} onChange={(value) => setSubscriptionForm((current) => ({ ...current, intervalMin: Number(value) || 0 }))} />
              </div>
              <ModalActions busy={Boolean(busy)} submitText={subscriptionForm.id ? '保存并刷新' : '添加订阅'} onCancel={() => setModal(null)} />
            </form>
          )}

          {modal === 'tunnel' && (
            <form onSubmit={submitTunnel}>
              <ModalHeader title={tunnelForm.editingName ? '编辑 SSH 隧道' : '新建 SSH 隧道'} onClose={() => setModal(null)} />
              <div className="form-grid">
                <FormField label="隧道名称" value={tunnelForm.name} onChange={(value) => setTunnelForm((current) => ({ ...current, name: value }))} required />
                <label className="form-field">
                  <span>类型</span>
                  <select value={tunnelForm.mode} onChange={(event) => setTunnelForm((current) => ({ ...current, mode: event.target.value as TunnelForm['mode'] }))}>
                    <option value="local">本地转发</option>
                    <option value="remote">远程转发</option>
                    <option value="dynamic">动态转发</option>
                  </select>
                </label>
              </div>
              <div className="form-grid">
                <FormField label="监听地址" value={tunnelForm.listen} onChange={(value) => setTunnelForm((current) => ({ ...current, listen: value }))} required />
                <FormField label="目标地址" value={tunnelForm.forward} onChange={(value) => setTunnelForm((current) => ({ ...current, forward: value }))} disabled={tunnelForm.mode === 'dynamic'} />
              </div>
              <div className="form-grid">
                <FormField label="SSH 地址" value={tunnelForm.sshAddr} onChange={(value) => setTunnelForm((current) => ({ ...current, sshAddr: value }))} required />
                <FormField label="SSH 用户" value={tunnelForm.sshUser} onChange={(value) => setTunnelForm((current) => ({ ...current, sshUser: value }))} required />
              </div>
              <FormField label="SSH 密码" type="password" value={tunnelForm.password} onChange={(value) => setTunnelForm((current) => ({ ...current, password: value }))} />
              <ModalActions busy={Boolean(busy)} submitText={tunnelForm.editingName ? '保存隧道' : '创建隧道'} onCancel={() => setModal(null)} />
            </form>
          )}

          {modal === 'ssh' && (
            <form onSubmit={submitSSHConnection}>
              <ModalHeader title={sshForm.id ? '编辑 SSH 连接' : '新建 SSH 连接'} onClose={() => setModal(null)} />
              <FormField label="连接名称" value={sshForm.name} onChange={(value) => setSSHForm((current) => ({ ...current, name: value }))} required />
              <div className="form-grid">
                <FormField label="SSH 地址" value={sshForm.addr} onChange={(value) => setSSHForm((current) => ({ ...current, addr: value }))} required />
                <FormField label="用户名" value={sshForm.user} onChange={(value) => setSSHForm((current) => ({ ...current, user: value }))} required />
              </div>
              <FormField label="密码" type="password" value={sshForm.password} onChange={(value) => setSSHForm((current) => ({ ...current, password: value }))} required />
              <div className="modal-danger-row">
                {sshForm.id && <button className="danger-soft" onClick={deleteSelectedSSH} type="button">删除连接</button>}
              </div>
              <ModalActions busy={Boolean(busy)} submitText={sshForm.id ? '保存连接' : '创建连接'} onCancel={() => setModal(null)} />
            </form>
          )}
        </div>
      </div>
    )
  }

  function renderNodesPage() {
    return (
      <section className="page-card nodes-page">
        <PageHeader title="节点" />
        <div className="nodes-page-grid">
          <div className="node-page-main">
            <div className="node-page-toolbar">
              <div className="node-current-selection">
                <span>当前选择</span>
                <strong>{selectedNode ? <><Flag code={selectedNode.flagCode} />{selectedNode.name}</> : '未选择节点'}</strong>
              </div>
              <div className="node-page-actions">
                <button className="primary-btn" disabled={Boolean(busy) || nodes.length === 0} onClick={testAllNodes} type="button">测试节点</button>
              </div>
            </div>
            {renderNodeCardGrid()}
          </div>
        </div>
      </section>
    )
  }

  function renderNodeCardGrid() {
    if (nodes.length === 0) {
      return <EmptyPanel text={backendReady ? '暂无节点，请先启用订阅' : '未连接后端，暂无节点数据'} />
    }

    return (
      <div className="node-card-grid">
        {nodes.map((node) => {
          const selected = selectedNode?.id === node.id
          const running = connected && proxyStatus?.node_name === node.name
          return (
            <button
              aria-pressed={selected}
              className={`node-card-item ${selected ? 'selected' : ''}`}
              key={node.id}
              onClick={() => connectNode(node)}
              type="button"
            >
              <span className="node-selected-bar" aria-hidden="true" />
              <span className="node-card-top">
                <span className="node-card-name"><Flag code={node.flagCode} /><strong title={node.name}>{node.name}</strong></span>
                <span className={running ? 'node-selected-label' : 'node-click-label'}>{running ? '使用中' : '点击连接'}</span>
              </span>
              <span className="node-card-meta">
                <span className="type-pill">{node.protocol}</span>
                <span>{node.country} · {node.city}</span>
              </span>
              <span className="node-card-address" title={`${node.server}:${node.port}`}>{node.server}:{node.port}</span>
              <span className="node-card-bottom">
                <span className="node-latency-label">网络延迟</span>
                <span className="node-latency-value">
                  <span className={`${nodeStatusClass(node)} node-card-status`}><span className={`dot ${nodeStatusDot(node)}`} />{nodeStatusText(node)}</span>
                  <strong className={latencyClass(node)} title={node.latencyError || undefined}>{formatLatency(node)}</strong>
                </span>
              </span>
            </button>
          )
        })}
      </div>
    )
  }

  function renderSubscriptionsPage() {
    return (
      <section className="page-card subscriptions-page">
        <PageHeader title="订阅" actions={<><button className="small-outline" onClick={() => openSubscriptionForm()} type="button">＋ 添加订阅</button><button className="small-outline" disabled={Boolean(busy) || subscriptions.length === 0} onClick={refreshAllSubs} type="button">↻ 更新所有</button><span className="subscription-summary">已启用 {subscriptions.filter((item) => item.enabled).length}/{subscriptions.length}</span></>} />
        <div className="subscription-list">
          {subscriptions.map((item) => (
            <div className="subscription-card" key={item.id}>
              <div>
                <h3>{item.name || '未命名订阅'}</h3>
                <p>{item.url || '-'}</p>
                <p>最后更新： {formatOptionalDate(item.last_refresh)}</p>
                <p>状态： <span className={item.enabled ? 'good-text' : 'muted-text'}>{item.enabled ? '已启用' : '未启用'}</span> · <span className={item.last_error ? 'danger-text' : 'good-text'}>{item.last_error ? '更新失败' : '更新成功'}</span></p>
              </div>
              <div className="subscription-meta">
                <InfoPair label="节点总数" value={item.node_count ?? item.tunnel_count ?? 0} />
                <InfoPair label="展示节点" value={item.enabled ? item.node_count ?? item.tunnel_count ?? 0 : 0} />
                <InfoPair label="自动刷新" value={item.auto_refresh ? '开启' : '关闭'} highlight={item.auto_refresh} />
              </div>
              <div className="subscription-actions">
                <button className={`big-switch ${item.enabled ? 'on' : ''}`} disabled={Boolean(busy)} onClick={() => toggleSub(item)} title={item.enabled ? '停用订阅' : '启用订阅'} type="button"><i /></button>
                <button className="small-outline" disabled={Boolean(busy)} onClick={() => refreshSub(item)} type="button">更新</button>
                <button className="small-outline" onClick={() => openSubscriptionForm(item)} type="button">编辑</button>
                <button className="ellipsis" onClick={() => deleteSub(item)} type="button">删除</button>
              </div>
            </div>
          ))}
          {subscriptions.length === 0 && <EmptyPanel text={backendReady ? '暂无订阅' : '未连接后端，暂无订阅数据'} />}
        </div>
        <PageFooter count={subscriptions.length} />
      </section>
    )
  }

  function renderTunnelsPage() {
    return (
      <section className="page-card tunnels-page">
        <PageHeader title="SSH 隧道" actions={<button className="small-outline" onClick={() => openTunnelForm()} type="button">＋ 新建隧道</button>} />
        <div className="tunnel-table">
          <div className="tunnel-table-head">
            <span>隧道名称</span>
            <span>类型</span>
            <span>本地地址</span>
            <span>远程地址</span>
            <span>状态</span>
            <span>操作</span>
          </div>
          {tunnelRows.map((row) => (
            <button className={`tunnel-table-row ${row.id === selectedTunnel?.id ? 'selected' : ''}`} key={row.id} onClick={() => setSelectedTunnelID(row.id)} type="button">
              <span><span className={`dot ${row.running ? 'green' : 'gray'}`} />{row.name}</span>
              <span>{row.type}</span>
              <span>{row.listen}</span>
              <span>{row.forward}</span>
              <span className={row.running ? 'mini-state running' : 'mini-state'}>{row.running ? '运行中' : '已停止'}</span>
              <span className="row-actions"><span onClick={(event) => { event.stopPropagation(); toggleTunnel(row) }}>▶</span><span onClick={(event) => { event.stopPropagation(); openTunnelForm(row) }}>···</span></span>
            </button>
          ))}
          {tunnelRows.length === 0 && <EmptyTable text="暂无 SSH 隧道" />}
        </div>
        <div className="tunnel-detail-panel">
          {selectedTunnel ? (
            <>
              <button className="plain-star detail-close" type="button">×</button>
              <h2>{selectedTunnel.name}</h2>
              <InfoPair label="类型" value={selectedTunnel.type} />
              <InfoPair label="本地地址" value={selectedTunnel.listen} />
              <InfoPair label="远程地址" value={selectedTunnel.forward} />
              <InfoPair label="SSH 服务器" value={selectedTunnel.endpoint} />
              <InfoPair label="状态" value={selectedTunnel.running ? '运行中' : '已停止'} highlight={selectedTunnel.running} />
              <InfoPair label="更新时间" value={selectedTunnel.updatedAt} />
              <div className="detail-action-row">
                <button className="outline-btn" onClick={() => openTunnelForm(selectedTunnel)} type="button">编辑</button>
                <button className="danger-outline" disabled={Boolean(busy)} onClick={() => toggleTunnel(selectedTunnel)} type="button">{selectedTunnel.running ? '停止' : '启动'}</button>
                <button className="danger-soft" disabled={Boolean(busy)} onClick={deleteSelectedTunnel} type="button">删除</button>
              </div>
            </>
          ) : (
            <EmptyState text="暂无隧道详情" />
          )}
        </div>
      </section>
    )
  }

  function renderTerminalPage() {
    return (
      <section className="terminal-page">
        <div className="card terminal-sidebar">
          <PageHeader title="连接管理" actions={<button className="small-outline" onClick={() => openSSHForm()} type="button">＋ 新建连接</button>} />
          <div className="terminal-connection-list">
            {sshRows.map((row) => (
              <button className={`terminal-connection ${row.id === selectedSSH?.id ? 'selected' : ''}`} key={row.id} onDoubleClick={() => openTerminal(row)} onClick={() => setSelectedSSHID(row.id)} type="button">
                <span className="terminal-icon">&gt;_</span>
                <span><strong>{row.name}</strong><small>{row.detail}</small></span>
              </button>
            ))}
            {sshRows.length === 0 && <EmptyList text="暂无 SSH 终端连接" />}
          </div>
        </div>
        <div className="card terminal-window-card">
          <div className="terminal-toolbar">
            <div className="terminal-tab-label">
              <span className={`dot ${terminalConnected ? 'green' : 'red'}`} />
              <span title={terminalLabel}>{terminalLabel}</span>
            </div>
            <div className="terminal-toolbar-actions">
              <button className="plain-star" onClick={() => openTerminal()} type="button">连接</button>
              <button className="plain-star" onClick={() => openSSHForm(selectedSSH)} type="button">编辑</button>
              <button className="terminal-tab-delete" disabled={!terminalSessionID && !terminalTitle} onClick={closeTerminal} title="断开并删除标签" aria-label="断开并删除标签" type="button">×</button>
            </div>
          </div>
          <div className="terminal-screen">
            {terminalSessionID ? (
              <SSHTerminalPane sessionID={terminalSessionID} title={terminalTitle || selectedSSH?.name || 'SSH'} />
            ) : selectedSSH ? (
              <pre>{`连接：${selectedSSH.detail}\n\n点击右上角“连接”打开终端会话。`}</pre>
            ) : (
              <span>请选择一个 SSH 连接</span>
            )}
          </div>
        </div>
      </section>
    )
  }

  function renderSettingsPage() {
    return (
      <section className="page-card settings-page">
        <div className="settings-tabs">
          {settingsTabs.map((item, index) => <button className={index === 0 ? 'active' : ''} key={item}>{item}</button>)}
        </div>
        <div className="settings-form">
          <h3>启动设置</h3>
          <div className="checkbox-row">
            <CheckItem checked={clientSettings.auto_start} label="开机启动" />
            <CheckItem checked label="启动后自动连接" />
            <CheckItem checked label="最小化到托盘" />
          </div>

          <h3>网络设置</h3>
          <div className="settings-grid">
            <Field label="本地端口" value={proxyStatus?.socks_addr ? portFromAddr(proxyStatus.socks_addr) : '-'} />
            <Field label="SOCKS5 端口" value={proxyStatus?.socks_addr ? portFromAddr(proxyStatus.socks_addr) : '-'} />
            <Field label="HTTP 代理端口" value={proxyStatus?.http_addr ? portFromAddr(proxyStatus.http_addr) : '-'} />
          </div>

          <h3>系统代理</h3>
          <div className="checkbox-row compact">
            <CheckItem checked={clientSettings.system_proxy} label="启用系统代理" />
            <ToggleRow label="绕过局域网和本地地址" />
          </div>

          <h3>TUN 模式</h3>
          <div className="checkbox-row compact">
            <CheckItem label="启用 TUN 模式（实验性功能）" />
            <ToggleRow label="" />
          </div>

          <p className="settings-data-dir">数据目录：{dataDir || '-'}</p>
        </div>
      </section>
    )
  }

  function renderLogsPage() {
    return (
      <section className="page-card logs-page">
        <div className="logs-toolbar">
          <SelectLike label="日志级别" value="全部" />
          <SelectLike label="时间范围" value="全部" />
          <SearchBox placeholder="搜索日志内容" />
          <button className="small-outline" type="button">导出日志</button>
          <button className="primary-btn" onClick={clearLogEntries} type="button">清空日志</button>
        </div>
        <div className="logs-table">
          <div className="logs-head">
            <span>时间</span>
            <span>级别</span>
            <span>模块</span>
            <span>内容</span>
          </div>
          {logs.map((item, index) => (
            <div className="logs-row" key={`${item.time}-${index}`}>
              <span>{formatOptionalDate(item.time)}</span>
              <span className={item.level === 'error' ? 'danger-text' : item.level === 'warn' ? 'warn-text' : 'good-text'}>{logLevelLabel(item.level)}</span>
              <span>{item.module}</span>
              <span>{item.message}</span>
            </div>
          ))}
          {logs.length === 0 && <EmptyTable text="暂无日志数据" />}
        </div>
        <PageFooter count={logs.length} />
      </section>
    )
  }

  function renderTunnelCompactCard() {
    return (
      <div className="card compact-card">
        <PanelTitle title="SSH 隧道" action={<button className="small-outline" onClick={() => openTunnelForm()} type="button">＋ 新建隧道</button>} />
        <div className="compact-list">
          {tunnelRows.map((row) => (
            <div className="compact-row" key={row.id}>
              <button className={`round-play ${row.running ? 'running' : ''}`} onClick={() => toggleTunnel(row)} type="button">▶</button>
              <div><strong>{row.name}</strong><p>{row.detail}</p></div>
              <span className={row.running ? 'mini-state running' : 'mini-state'}><span className={`dot ${row.running ? 'green' : 'gray'}`} />{row.running ? '运行中' : '已停止'}</span>
              <button className="ellipsis" type="button">···</button>
            </div>
          ))}
          {tunnelRows.length === 0 && <EmptyList text="暂无 SSH 隧道" />}
        </div>
        <button className="link-btn" onClick={() => setActiveNav('tunnels')} type="button">查看全部隧道 →</button>
      </div>
    )
  }

  function renderTerminalCompactCard() {
    return (
      <div className="card compact-card">
        <PanelTitle title="SSH 终端连接" action={<button className="small-outline" onClick={() => openSSHForm()} type="button">＋ 新建连接</button>} />
        <div className="compact-list">
          {sshRows.map((row) => (
            <div className="compact-row terminal-row" key={row.id}>
              <span className="terminal-icon">&gt;_</span>
              <div><strong>{row.name}</strong><p>{row.detail}</p></div>
              <span className="last-connect">上次连接：{row.last}</span>
              <button className="blue-play" onClick={() => openTerminal(row)} type="button">▶</button>
            </div>
          ))}
          {sshRows.length === 0 && <EmptyList text="暂无 SSH 终端连接" />}
        </div>
        <button className="link-btn" onClick={() => setActiveNav('terminal')} type="button">查看全部连接 →</button>
      </div>
    )
  }
}

function PageHeader({ title, actions }: { title: string; actions?: React.ReactNode }) {
  return (
    <div className="page-header">
      <h1>{title}</h1>
      <div className="page-actions">{actions}</div>
    </div>
  )
}

function ModalHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return (
    <div className="modal-header">
      <h2>{title}</h2>
      <button onClick={onClose} type="button">×</button>
    </div>
  )
}

function ModalActions({ submitText, busy, onCancel }: { submitText: string; busy: boolean; onCancel: () => void }) {
  return (
    <div className="modal-actions">
      <button className="outline-btn" onClick={onCancel} type="button">取消</button>
      <button className="primary-btn" disabled={busy} type="submit">{submitText}</button>
    </div>
  )
}

function FormField({
  label,
  value,
  onChange,
  type = 'text',
  required = false,
  disabled = false,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  type?: string
  required?: boolean
  disabled?: boolean
}) {
  return (
    <label className="form-field">
      <span>{label}</span>
      <input disabled={disabled} required={required} type={type} value={value} onChange={(event) => onChange(event.target.value)} />
    </label>
  )
}

function PageFooter({ count }: { count: number }) {
  return (
    <div className="page-footer">
      <span>共 {count} 条</span>
      <div className="pager"><button>‹</button><button className="active">1</button><button>›</button><button>10 条/页⌄</button></div>
    </div>
  )
}

function PanelTitle({ title, action }: { title: string; action?: React.ReactNode }) {
  return (
    <div className="panel-title">
      <h2>{title}</h2>
      {action}
    </div>
  )
}

function MetricBlock({ label, value, icon, title }: { label: string; value: string; icon: 'up' | 'down' | 'clock'; title?: string }) {
  return (
    <div className="metric-block" title={title}>
      <p>{label}</p>
      <strong><MetricIcon name={icon} />{value}</strong>
    </div>
  )
}

function ToggleRow({ label, checked = false, disabled = false, title, onToggle }: { label: string; checked?: boolean; disabled?: boolean; title?: string; onToggle?: () => void }) {
  return (
    <div className="toggle-row">
      <span>{label}</span>
      <button aria-pressed={checked} className={`toggle ${checked ? 'checked' : ''}`} disabled={disabled || !onToggle} onClick={onToggle} title={title} type="button"><i /></button>
    </div>
  )
}

function CheckItem({ label, checked = false }: { label: string; checked?: boolean }) {
  return <label className="check-item"><span className={checked ? 'checkbox checked' : 'checkbox'} />{label}</label>
}

function Field({ label, value }: { label: string; value: string }) {
  return <label className="field"><span>{label}</span><input readOnly value={value} /></label>
}

function SearchBox({ placeholder }: { placeholder: string }) {
  return <label className="search-box"><span>⌕</span><input readOnly placeholder={placeholder} /></label>
}

function SelectLike({ label, value }: { label: string; value: string }) {
  return <label className="select-like"><span>{label}</span><button>{value}⌄</button></label>
}

function InfoPair({
  label,
  value,
  highlight = false,
  faded = false,
  danger = false,
}: {
  label: string
  value: string | number
  highlight?: boolean
  faded?: boolean
  danger?: boolean
}) {
  return (
    <div className="info-pair">
      <span>{label}</span>
      <strong className={`${highlight ? 'highlight' : ''} ${faded ? 'faded' : ''} ${danger ? 'danger-text' : ''}`}>{value}</strong>
    </div>
  )
}

function EmptyTable({ text }: { text: string }) {
  return <div className="table-empty">{text}</div>
}

function EmptyState({ text }: { text: string }) {
  return <div className="empty-state">{text}</div>
}

function EmptyList({ text }: { text: string }) {
  return <div className="list-empty">{text}</div>
}

function EmptyPanel({ text }: { text: string }) {
  return <div className="empty-panel">{text}</div>
}

function Flag({ code }: { code: string }) {
  return <span className={`flag-box flag-${code}`} aria-hidden="true" />
}

function BrandLogo({ size = 'normal' }: { size?: 'normal' | 'large' | 'xl' }) {
  return <img className={`brand-logo ${size}`} src={logoSrc} alt="" aria-hidden="true" />
}

function Icon({ name }: { name: string }) {
  return <span className={`nav-icon ${name}`} aria-hidden="true" />
}

function MetricIcon({ name }: { name: 'up' | 'down' | 'clock' }) {
  return <span className={`metric-icon ${name}`} aria-hidden="true" />
}

function toDashboardNode(node: proxysubscription.ProxyNode, index: number, result?: proxycore.TestResult, testing = false): DashboardNode {
  const location = inferLocation(node.name, node.server)
  const protocol = protocolLabel(node.protocol)
  const latency = result?.ok ? result.latency_ms ?? 0 : 0
  const latencyState = testing ? 'testing' : result ? (result.ok ? 'ok' : 'failed') : 'untested'
  const latencyError = result && !result.ok ? result.error || '测试失败' : ''
  const speed = result?.ok ? result.speed_mbps ?? 0 : 0
  return {
    id: node.id || `${node.name}-${index}`,
    name: node.name || `${location.country} - ${location.city} - 01`,
    country: location.country,
    city: location.city,
    flagCode: location.flagCode,
    protocol,
    server: node.server,
    port: node.port,
    method: node.method || node.security || '',
    password: node.password || node.uuid || '',
    security: node.security || node.flow || node.tls?.server_name || '',
    udp: Boolean(node.udp),
    latency,
    latencyState,
    latencyError,
    speed,
    source: node,
  }
}

function topLatencyNodes(nodes: DashboardNode[]) {
  const rank = (node: DashboardNode) => {
    if (node.latencyState === 'ok' && node.latency > 0) {
      return 0
    }
    if (node.latencyState === 'testing') {
      return 1
    }
    if (node.latencyState === 'untested') {
      return 2
    }
    return 3
  }
  return [...nodes]
    .sort((a, b) => rank(a) - rank(b) || (a.latency || Number.MAX_SAFE_INTEGER) - (b.latency || Number.MAX_SAFE_INTEGER) || a.name.localeCompare(b.name))
    .slice(0, 3)
}

function toTunnelRow(item: manager.Status): TunnelRow {
  const mode = normalizeMode(item.config.mode)
  const listen = item.config.listen || '-'
  const forward = mode === 'dynamic' ? 'SOCKS5' : item.config.forward || '-'
  const type = mode === 'remote' ? '远程转发' : mode === 'dynamic' ? '动态转发' : '本地转发'
  return {
    id: item.config.name,
    name: item.config.name,
    type,
    listen,
    forward,
    detail: mode === 'dynamic' ? `${listen} (${forward})` : `${listen} → ${forward}`,
    endpoint: `${item.config.ssh?.user || '-'}@${item.config.ssh?.addr || '-'}`,
    running: item.state === 'running',
    updatedAt: item.started_at ? formatOptionalDate(item.started_at) : '-',
    source: item,
  }
}

function toSSHRow(item: store.SSHConnection, index: number): SSHRow {
  return {
    id: item.id || `${item.addr}-${index}`,
    name: item.name || `VPS - ${index + 1}`,
    detail: `${item.user || 'root'}@${item.addr || '-'}`,
    last: '-',
    source: item,
  }
}

function buildTunnelConfig(form: TunnelForm): config.TunnelCfg {
  return {
    name: form.name.trim(),
    mode: form.mode,
    ssh: {
      addr: form.sshAddr.trim(),
      user: form.sshUser.trim(),
      identity_file: '',
      passphrase: '',
      password: form.password,
    },
    listen: form.listen.trim(),
    forward: form.mode === 'dynamic' ? '' : form.forward.trim(),
    transport: 'tcp',
    tun: {
      subnet: '',
      dns: [],
      auto_route: false,
      tls_cert: '',
      tls_key: '',
      sni: '',
      pin_sha256: '',
    },
  } as unknown as config.TunnelCfg
}

function sshRowLabel(row: SSHRow) {
  return formatSSHConnectionLabel(row.name, hostFromAddr(row.source?.addr || ''))
}

function formatSSHConnectionLabel(name: string, host: string) {
  const label = name || '未命名连接'
  const displayHost = host || '-'
  return `${label} (${displayHost})`
}

function hostFromAddr(addr: string) {
  const value = (addr || '').trim()
  if (!value) {
    return ''
  }
  if (value.startsWith('[')) {
    const end = value.indexOf(']')
    return end > 0 ? value.slice(1, end) : value
  }
  const parts = value.split(':')
  if (parts.length === 2) {
    return parts[0] || value
  }
  return value
}

function SSHTerminalPane({ sessionID, title }: { sessionID: string; title: string }) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)

  useEffect(() => {
    if (!containerRef.current || terminalRef.current) {
      return
    }
    const container = containerRef.current
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: 'Consolas, "Cascadia Mono", "Courier New", monospace',
      fontSize: 13,
      scrollback: 5000,
      theme: {
        background: '#050505',
        foreground: '#f6f7fb',
        cursor: '#22c55e',
        selectionBackground: '#334155',
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    terminalRef.current = term
    fitAddonRef.current = fit
    term.writeln(`SafeLink SSH Terminal - ${title}`)

    const writeClipboard = async (text: string) => {
      if (!text) {
        return
      }
      try {
        await navigator.clipboard.writeText(text)
      } catch {
        const textarea = document.createElement('textarea')
        textarea.value = text
        textarea.style.position = 'fixed'
        textarea.style.opacity = '0'
        document.body.appendChild(textarea)
        textarea.select()
        document.execCommand('copy')
        textarea.remove()
      }
    }
    const readClipboard = async () => {
      try {
        return await navigator.clipboard.readText()
      } catch {
        return ''
      }
    }
    const copySelection = () => {
      const selection = term.getSelection()
      if (selection) {
        writeClipboard(selection).catch(() => undefined)
        term.clearSelection()
      }
    }
    const pasteText = (text: string) => {
      if (text) {
        SendSSHInput(sessionID, text).catch((err) => term.writeln(`\r\n[错误] ${errorMessage(err)}`))
      }
    }
    const pasteClipboard = () => {
      readClipboard().then(pasteText).catch(() => undefined)
    }

    const dataDisposable = term.onData((data) => {
      SendSSHInput(sessionID, data).catch((err) => term.writeln(`\r\n[错误] ${errorMessage(err)}`))
    })
    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown') {
        return true
      }
      const key = event.key.toLowerCase()
      const copyShortcut = (event.ctrlKey || event.metaKey) && key === 'c'
      const pasteShortcut = (event.ctrlKey || event.metaKey) && key === 'v'
      if (copyShortcut && term.hasSelection()) {
        event.preventDefault()
        copySelection()
        return false
      }
      if (pasteShortcut) {
        event.preventDefault()
        pasteClipboard()
        return false
      }
      return true
    })
    const handleCopy = (event: ClipboardEvent) => {
      const selection = term.getSelection()
      if (!selection) {
        return
      }
      event.preventDefault()
      event.clipboardData?.setData('text/plain', selection)
      term.clearSelection()
    }
    const handlePaste = (event: ClipboardEvent) => {
      const text = event.clipboardData?.getData('text/plain') || ''
      if (!text) {
        return
      }
      event.preventDefault()
      pasteText(text)
    }
    const handleContextMenu = (event: MouseEvent) => {
      event.preventDefault()
      if (term.hasSelection()) {
        copySelection()
      } else {
        pasteClipboard()
      }
      term.focus()
    }
    container.addEventListener('copy', handleCopy)
    container.addEventListener('paste', handlePaste)
    container.addEventListener('contextmenu', handleContextMenu)
    const offOutput = EventsOn('ssh:output', (event: SSHOutputEvent) => {
      if (event?.session_id === sessionID) {
        term.write(event.data)
      }
    })
    const offClosed = EventsOn('ssh:closed', (event: SSHClosedEvent) => {
      if (event?.session_id === sessionID) {
        term.writeln(`\r\n[连接已关闭${event.message ? `：${event.message}` : ''}]`)
      }
    })
    const resize = () => {
      fit.fit()
      ResizeSSHSession(sessionID, term.rows, term.cols).catch(() => undefined)
    }
    window.addEventListener('resize', resize)
    window.setTimeout(resize, 0)

    return () => {
      dataDisposable.dispose()
      container.removeEventListener('copy', handleCopy)
      container.removeEventListener('paste', handlePaste)
      container.removeEventListener('contextmenu', handleContextMenu)
      offOutput()
      offClosed()
      window.removeEventListener('resize', resize)
      term.dispose()
      terminalRef.current = null
      fitAddonRef.current = null
    }
  }, [sessionID, title])

  return <div className="terminal-xterm" ref={containerRef} tabIndex={0} onPointerDown={() => terminalRef.current?.focus()} />
}

function inferLocation(name: string, server: string) {
  const text = `${name} ${server}`.toLowerCase()
  const rules = [
    { keys: ['jp', 'japan', 'tokyo', '日本'], flagCode: 'jp', country: '日本', city: 'Tokyo' },
    { keys: ['sg', 'singapore', '新加坡'], flagCode: 'sg', country: '新加坡', city: 'Singapore' },
    { keys: ['hk', 'hong kong', '香港'], flagCode: 'hk', country: '香港', city: 'Hong Kong' },
    { keys: ['us', 'usa', 'america', 'los angeles', '美国'], flagCode: 'us', country: '美国', city: 'Los Angeles' },
    { keys: ['de', 'germany', 'frankfurt', '德国'], flagCode: 'de', country: '德国', city: 'Frankfurt' },
    { keys: ['uk', 'gb', 'london', '英国'], flagCode: 'gb', country: '英国', city: 'London' },
    { keys: ['au', 'australia', 'sydney', '澳大利亚'], flagCode: 'au', country: '澳大利亚', city: 'Sydney' },
    { keys: ['tw', 'taiwan', 'taipei', '台湾'], flagCode: 'tw', country: '台湾', city: 'Taipei' },
  ]
  return rules.find((rule) => rule.keys.some((key) => text.includes(key))) ?? { flagCode: 'jp', country: '日本', city: 'Tokyo' }
}

function protocolLabel(value: string) {
  const lower = value.toLowerCase()
  if (lower.includes('vmess')) {
    return 'VMess'
  }
  if (lower.includes('vless')) {
    return 'VLESS'
  }
  if (lower.includes('trojan')) {
    return 'Trojan'
  }
  return value || 'Shadowsocks'
}

function normalizeMode(mode: string) {
  return mode === 'remote' || mode === 'dynamic' || mode === 'vpn' ? mode : 'local'
}

function normalizeProxyMode(mode: string): ProxyMode {
  return mode === 'global' || mode === 'direct' ? mode : 'rule'
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return '0 B'
  }
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 2)} ${units[unit]}`
}

function formatByteRate(value: number) {
  return `${formatBytes(value)}/s`
}

function formatLatency(node?: DashboardNode) {
  if (!node) {
    return '-'
  }
  if (node.latencyState === 'failed') {
    return '不可用'
  }
  if (node.latencyState === 'testing') {
    return '测试中'
  }
  if (node.latencyState === 'ok' && node.latency > 0) {
    return `${node.latency} ms`
  }
  return '-'
}

function latencyClass(node: DashboardNode) {
  if (node.latencyState === 'failed') {
    return 'danger-text'
  }
  if (node.latencyState === 'testing') {
    return 'warn-text'
  }
  if (node.latency <= 0) {
    return 'muted-text'
  }
  return node.latency > 150 ? 'warn-text' : 'good-text'
}

function nodeStatusText(node: DashboardNode) {
  if (node.latencyState === 'ok') {
    return '可用'
  }
  if (node.latencyState === 'failed') {
    return '失败'
  }
  if (node.latencyState === 'testing') {
    return '测试中'
  }
  return '未测试'
}

function nodeStatusClass(node: DashboardNode) {
  if (node.latencyState === 'ok') {
    return 'available'
  }
  if (node.latencyState === 'failed') {
    return 'node-status failed'
  }
  if (node.latencyState === 'testing') {
    return 'node-status testing'
  }
  return 'node-status untested'
}

function nodeStatusDot(node: DashboardNode) {
  if (node.latencyState === 'ok') {
    return 'green'
  }
  if (node.latencyState === 'failed') {
    return 'red'
  }
  if (node.latencyState === 'testing') {
    return 'yellow'
  }
  return 'gray'
}

function formatClockDuration(startedAt: string) {
  const started = new Date(startedAt).getTime()
  if (!Number.isFinite(started)) {
    return '00:00:00'
  }
  const seconds = Math.max(0, Math.floor((Date.now() - started) / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60
  return [hours, minutes, rest].map((part) => String(part).padStart(2, '0')).join(':')
}

function formatDateTime(value: string) {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) {
    return value
  }
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function formatOptionalDate(value?: string) {
  return value ? formatDateTime(value) : '-'
}

function logLevelLabel(level: string) {
  if (level === 'error') {
    return '错误'
  }
  if (level === 'warn') {
    return '警告'
  }
  return '信息'
}

function portFromAddr(addr: string) {
  return addr.split(':').pop() || '-'
}

function isBackendReady() {
  return Boolean(window.go?.main?.App)
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

export default App
