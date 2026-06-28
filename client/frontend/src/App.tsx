import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import {
  AddTunnel,
  CheckDriver,
  CloseSSHSession,
  CreateSSHSession,
  DeleteSSHConnection,
  DeleteSubscription,
  DeleteTunnel,
  GetDataDir,
  GetVersion,
  ImportSubscription,
  InstallDriver,
  IsRunningAsAdmin,
  ListProxyNodes,
  ListSSHConnections,
  ListSubscriptions,
  ListTunnels,
  ProxyStatus,
  RestartTunnel,
  RequestAdminRestart,
  ResizeSSHSession,
  SaveSSHConnection,
  SendSSHInput,
  StartProxyNode,
  StartTunnel,
  StopProxy,
  StopTunnel,
  ToggleRoute,
  UpdateTunnel,
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'
import type { config, manager, proxycore, proxysubscription, store, tunnel } from '../wailsjs/go/models'

type TunnelMode = 'local' | 'remote' | 'dynamic' | 'vpn'
type TabKey = 'dashboard' | 'vpn' | 'proxy' | 'ssh' | 'sshTerminal' | 'subscriptions' | 'driver' | 'settings'

type TunnelForm = {
  name: string
  mode: TunnelMode
  sshAddr: string
  sshUser: string
  identityFile: string
  passphrase: string
  password: string
  listen: string
  forward: string
  transport: string
  subnet: string
  dns: string
  autoRoute: boolean
  tlsCert: string
  tlsKey: string
  sni: string
  pinSHA256: string
  padding: 'default' | 'enabled' | 'disabled'
}

type SSHTerminalForm = {
  id: string
  name: string
  addr: string
  user: string
  identityFile: string
  passphrase: string
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

type SSHErrorEvent = {
  session_id: string
  message: string
}

type TerminalWindow = {
  localId: string
  sessionId: string
  title: string
  status: string
  connection: store.SSHConnection
}

const emptyForm: TunnelForm = {
  name: '',
  mode: 'local',
  sshAddr: '',
  sshUser: '',
  identityFile: '',
  passphrase: '',
  password: '',
  listen: '127.0.0.1:1080',
  forward: '',
  transport: 'tcp',
  subnet: '10.8.0.2/24',
  dns: '1.1.1.1,8.8.8.8',
  autoRoute: false,
  tlsCert: '',
  tlsKey: '',
  sni: '',
  pinSHA256: '',
  padding: 'default',
}

const emptyTerminalForm: SSHTerminalForm = {
  id: '',
  name: '',
  addr: '',
  user: '',
  identityFile: '',
  passphrase: '',
  password: '',
}

const modeLabels: Record<TunnelMode, string> = {
  local: '本地转发',
  remote: '远程转发',
  dynamic: 'SOCKS 代理',
  vpn: 'VPN',
}

const tabs: Array<{ key: TabKey; label: string }> = [
  { key: 'dashboard', label: '概览' },
  { key: 'vpn', label: 'VPN' },
  { key: 'proxy', label: '代理' },
  { key: 'ssh', label: 'SSH 隧道' },
  { key: 'sshTerminal', label: 'SSH 终端' },
  { key: 'subscriptions', label: '订阅' },
  { key: 'driver', label: '驱动' },
  { key: 'settings', label: '设置' },
]

const sshModes: TunnelMode[] = ['local', 'remote', 'dynamic']

function isBackendReady() {
  return Boolean(window.go?.main?.App)
}

function formatSSHTerminalTitle(conn: store.SSHConnection): string {
  const label = conn.name || `${conn.user}@${conn.addr}`
  return conn.name ? `${conn.name} (${conn.addr})` : label
}

function isTerminalWindowConnected(status: string): boolean {
  return status === '已连接'
}

function App() {
  const [activeTab, setActiveTab] = useState<TabKey>('dashboard')
  const [version, setVersion] = useState('1.0.0')
  const [dataDir, setDataDir] = useState('')
  const [tunnels, setTunnels] = useState<manager.Status[]>([])
  const [subscriptions, setSubscriptions] = useState<store.SubscriptionSource[]>([])
  const [proxyNodes, setProxyNodes] = useState<proxysubscription.ProxyNode[]>([])
  const [proxyStatus, setProxyStatus] = useState<proxycore.Status | null>(null)
  const [sshConnections, setSSHConnections] = useState<store.SSHConnection[]>([])
  const [driverStatus, setDriverStatus] = useState<tunnel.DriverStatus | null>(null)
  const [isAdmin, setIsAdmin] = useState(true)
  const [form, setForm] = useState<TunnelForm>(emptyForm)
  const [editingName, setEditingName] = useState('')
  const [terminalForm, setTerminalForm] = useState<SSHTerminalForm>(emptyTerminalForm)
  const [showTerminalForm, setShowTerminalForm] = useState(false)
  const [terminalWindows, setTerminalWindows] = useState<TerminalWindow[]>([])
  const [activeTerminalWindowID, setActiveTerminalWindowID] = useState('')
  const [terminalTabMenu, setTerminalTabMenu] = useState<{ x: number; y: number; win: TerminalWindow } | null>(null)
  const [subName, setSubName] = useState('')
  const [subUrl, setSubUrl] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')

  const backendReady = isBackendReady()

  const showError = useCallback((err: unknown) => {
    const message = err instanceof Error ? err.message : String(err)
    setError(message)
  }, [])

  const refresh = useCallback(async () => {
    if (!isBackendReady()) {
      setLoading(false)
      return
    }

    try {
      const [nextVersion, nextTunnels, nextSubscriptions, nextProxyNodes, nextProxyStatus, nextSSHConnections, nextDataDir, nextIsAdmin] = await Promise.all([
        GetVersion(),
        ListTunnels(),
        ListSubscriptions(),
        ListProxyNodes(),
        ProxyStatus(),
        ListSSHConnections(),
        GetDataDir(),
        IsRunningAsAdmin(),
      ])
      setVersion(nextVersion)
      setTunnels(nextTunnels ?? [])
      setSubscriptions(nextSubscriptions ?? [])
      setProxyNodes(nextProxyNodes ?? [])
      setProxyStatus(nextProxyStatus ?? null)
      setSSHConnections(nextSSHConnections ?? [])
      setDataDir(nextDataDir)
      setIsAdmin(nextIsAdmin)
      setError('')
    } catch (err) {
      showError(err)
    } finally {
      setLoading(false)
    }
  }, [showError])

  useEffect(() => {
    refresh()
    const timer = window.setInterval(refresh, 3000)
    return () => window.clearInterval(timer)
  }, [refresh])

  useEffect(() => {
    if (!terminalTabMenu) {
      return
    }
    const close = () => setTerminalTabMenu(null)
    const timer = window.setTimeout(() => {
      document.addEventListener('click', close)
      document.addEventListener('scroll', close, true)
    }, 0)
    return () => {
      window.clearTimeout(timer)
      document.removeEventListener('click', close)
      document.removeEventListener('scroll', close, true)
    }
  }, [terminalTabMenu])

  const totals = useMemo(() => {
    return tunnels.reduce(
      (acc, item) => {
        acc.bytesIn += item.stats?.bytes_in ?? 0
        acc.bytesOut += item.stats?.bytes_out ?? 0
        acc.active += item.stats?.conn_active ?? 0
        acc.total += item.stats?.conn_total ?? 0
        if (item.state === 'running') {
          acc.running += 1
        }
        if (item.state === 'connecting') {
          acc.connecting += 1
        }
        return acc
      },
      { bytesIn: 0, bytesOut: 0, active: 0, total: 0, running: 0, connecting: 0 },
    )
  }, [tunnels])

  const vpnTunnels = useMemo(() => tunnels.filter((item) => normalizeMode(item.config.mode) === 'vpn'), [tunnels])
  const sshTunnels = useMemo(() => tunnels.filter((item) => normalizeMode(item.config.mode) !== 'vpn'), [tunnels])

  async function runAction(label: string, action: () => Promise<unknown>) {
    if (!backendReady) {
      setError('当前页面未连接 Wails 后端，请从 Wails 桌面窗口或 http://localhost:34115 打开。')
      return
    }

    setBusy(label)
    setNotice('')
    setError('')
    try {
      await action()
      await refresh()
      setNotice(`${label}成功`)
    } catch (err) {
      showError(err)
    } finally {
      setBusy('')
    }
  }

  function startCreate(mode: TunnelMode = 'local') {
    setEditingName('')
    setForm({ ...emptyForm, mode })
    setActiveTab(mode === 'vpn' ? 'vpn' : 'ssh')
  }

  function startEdit(item: manager.Status) {
    const cfg = item.config
    setEditingName(cfg.name)
    setForm({
      name: cfg.name,
      mode: normalizeMode(cfg.mode),
      sshAddr: cfg.ssh?.addr ?? '',
      sshUser: cfg.ssh?.user ?? '',
      identityFile: cfg.ssh?.identity_file ?? '',
      passphrase: cfg.ssh?.passphrase ?? '',
      password: cfg.ssh?.password ?? '',
      listen: cfg.listen ?? '',
      forward: cfg.forward ?? '',
      transport: cfg.transport || 'tcp',
      subnet: cfg.tun?.subnet ?? '',
      dns: (cfg.tun?.dns ?? []).join(','),
      autoRoute: Boolean(cfg.tun?.auto_route),
      tlsCert: cfg.tun?.tls_cert ?? '',
      tlsKey: cfg.tun?.tls_key ?? '',
      sni: cfg.tun?.sni ?? '',
      pinSHA256: cfg.tun?.pin_sha256 ?? '',
      padding: cfg.tun?.padding === true ? 'enabled' : cfg.tun?.padding === false ? 'disabled' : 'default',
    })
    setActiveTab(normalizeMode(cfg.mode) === 'vpn' ? 'vpn' : 'ssh')
  }

  async function submitTunnel(event: FormEvent) {
    event.preventDefault()
    const cfg = buildTunnelConfig(form)
    const label = editingName ? '更新隧道' : '新增隧道'

    await runAction(label, async () => {
      if (editingName) {
        await UpdateTunnel(editingName, cfg)
      } else {
        await AddTunnel(cfg)
      }
      setForm({ ...emptyForm, mode: form.mode })
      setEditingName('')
    })
  }

  async function submitSubscription(event: FormEvent) {
    event.preventDefault()
    await runAction('导入订阅', async () => {
      await ImportSubscription(subName.trim(), subUrl.trim())
      setSubName('')
      setSubUrl('')
    })
  }

  async function requestAdminRestart() {
    if (!window.confirm('创建 TUN 虚拟网卡需要管理员权限。SafeLink 将以管理员身份重新启动，当前窗口会关闭。是否继续？')) {
      return
    }

    setBusy('请求管理员权限')
    setNotice('')
    setError('')
    try {
      await RequestAdminRestart()
      setNotice('已请求管理员权限，正在启动新实例，请在新窗口或 UAC 对话框中继续。')
    } catch (err) {
      showError(err)
    } finally {
      setBusy('')
    }
  }

  async function checkDriver() {
    await runAction('检测驱动', async () => {
      setDriverStatus(await CheckDriver())
    })
  }

  async function installDriver() {
    await runAction('安装驱动', async () => {
      await InstallDriver()
      setDriverStatus(await CheckDriver())
    })
  }

  async function saveTerminalConnection(event: FormEvent) {
    event.preventDefault()
    if (!backendReady) {
      setError('当前页面未连接 Wails 后端，请从 Wails 桌面窗口打开。')
      return
    }

    setBusy('保存 SSH 连接')
    setError('')
    setNotice('')
    try {
      const saved = await SaveSSHConnection({
        id: terminalForm.id,
        name: terminalForm.name.trim(),
        addr: terminalForm.addr.trim(),
        user: terminalForm.user.trim(),
        password: terminalForm.password,
      })
      setSSHConnections((current) => upsertSSHConnection(current, saved))
      setTerminalForm(emptyTerminalForm)
      setShowTerminalForm(false)
      setNotice('SSH 连接已保存')
    } catch (err) {
      showError(err)
    } finally {
      setBusy('')
    }
  }

  async function openTerminalWindow(conn: store.SSHConnection, replaceLocalId?: string) {
    if (!backendReady) {
      setError('当前页面未连接 Wails 后端，请从 Wails 桌面窗口打开。')
      return
    }
    setBusy('连接 SSH 终端')
    setError('')
    try {
      const sessionId = await CreateSSHSession({
        addr: conn.addr,
        user: conn.user,
        identity_file: '',
        passphrase: '',
        password: conn.password,
        rows: 24,
        cols: 80,
      })
      const nextWindow: TerminalWindow = {
        localId: replaceLocalId || newLocalID(),
        sessionId,
        title: formatSSHTerminalTitle(conn),
        status: '已连接',
        connection: conn,
      }
      setTerminalWindows((current) => {
        if (!replaceLocalId) {
          return [...current, nextWindow]
        }
        return current.map((item) => (item.localId === replaceLocalId ? nextWindow : item))
      })
      setActiveTerminalWindowID(nextWindow.localId)
    } catch (err) {
      showError(err)
    } finally {
      setBusy('')
    }
  }

  async function reconnectTerminalWindow(win: TerminalWindow) {
    await CloseSSHSession(win.sessionId).catch(() => undefined)
    setTerminalWindows((current) => current.map((item) => (item.localId === win.localId ? { ...item, status: '重连中...' } : item)))
    await openTerminalWindow(win.connection, win.localId)
  }

  async function closeTerminalWindow(win: TerminalWindow) {
    await CloseSSHSession(win.sessionId).catch(() => undefined)
    setTerminalWindows((current) => current.filter((item) => item.localId !== win.localId))
    if (activeTerminalWindowID === win.localId) {
      const next = terminalWindows.find((item) => item.localId !== win.localId)
      setActiveTerminalWindowID(next?.localId || '')
    }
  }

  async function deleteSSHConnection(conn: store.SSHConnection) {
    if (!window.confirm(`确定删除 SSH 连接 "${conn.name}" 吗？`)) {
      return
    }
    await runAction('删除 SSH 连接', async () => {
      await DeleteSSHConnection(conn.id)
      setSSHConnections((current) => current.filter((item) => item.id !== conn.id))
    })
  }

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <div className="flex min-h-screen">
        <aside className="w-60 border-r border-slate-800 bg-slate-900/80 p-5">
          <div className="mb-8">
            <h1 className="text-2xl font-bold tracking-tight">SafeLink</h1>
            <p className="mt-1 text-sm text-slate-400">v{version}</p>
          </div>

          <nav className="space-y-2">
            {tabs.map((tab) => (
              <button
                key={tab.key}
                className={`w-full rounded-lg px-4 py-2 text-left text-sm transition ${
                  activeTab === tab.key
                    ? 'bg-cyan-500 text-slate-950'
                    : 'text-slate-300 hover:bg-slate-800 hover:text-white'
                }`}
                onClick={() => setActiveTab(tab.key)}
                type="button"
              >
                {tab.label}
              </button>
            ))}
          </nav>

          <div className="mt-8 rounded-xl border border-slate-800 bg-slate-950 p-4 text-xs text-slate-400">
            <p className="font-medium text-slate-300">后端状态</p>
            <p className={backendReady ? 'mt-2 text-emerald-400' : 'mt-2 text-amber-400'}>
              {backendReady ? 'Wails 已连接' : '浏览器预览模式'}
            </p>
          </div>
        </aside>

        <main className="flex-1 overflow-y-auto p-6">
          <header className="mb-6 flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="text-2xl font-semibold">{tabs.find((tab) => tab.key === activeTab)?.label}</h2>
              <p className="mt-1 text-sm text-slate-400">{pageDescription(activeTab)}</p>
            </div>
            <div className="flex gap-2">
              <button className="btn-secondary" onClick={refresh} type="button" disabled={Boolean(busy)}>
                刷新
              </button>
              {activeTab === 'dashboard' && (
                <>
                  <button className="btn-secondary" onClick={() => startCreate('vpn')} type="button">
                    新增 VPN
                  </button>
                  <button className="btn-primary" onClick={() => startCreate('local')} type="button">
                    新增 SSH 隧道
                  </button>
                </>
              )}
              {activeTab === 'vpn' && (
                <button className="btn-primary" onClick={() => startCreate('vpn')} type="button">
                  新增 VPN
                </button>
              )}
              {activeTab === 'ssh' && (
                <button className="btn-primary" onClick={() => startCreate('local')} type="button">
                  新增 SSH 隧道
                </button>
              )}
            </div>
          </header>

          {!backendReady && (
            <div className="mb-4 rounded-xl border border-amber-500/40 bg-amber-500/10 p-4 text-sm text-amber-200">
              当前是纯前端预览，不能调用 Go 后端。请从 Wails 桌面窗口访问，或打开 Wails 提示的
              http://localhost:34115。
            </div>
          )}
          {notice && <div className="mb-4 rounded-xl border border-emerald-500/30 bg-emerald-500/10 p-4 text-sm text-emerald-200">{notice}</div>}
          {error && <div className="mb-4 rounded-xl border border-rose-500/30 bg-rose-500/10 p-4 text-sm text-rose-200">{error}</div>}
          {loading ? <Panel>正在加载客户端状态...</Panel> : renderTab()}
        </main>
      </div>
    </div>
  )

  function renderTab() {
    switch (activeTab) {
      case 'dashboard':
        return (
          <div className="space-y-6">
            <div className="grid gap-4 md:grid-cols-4">
              <Metric label="隧道总数" value={tunnels.length} />
              <Metric label="运行中" value={totals.running} accent="text-emerald-300" />
              <Metric label="代理节点" value={proxyNodes.length} accent="text-cyan-300" />
              <Metric label="代理状态" value={proxyStatus?.state === 'running' ? '运行中' : '已停止'} accent={proxyStatus?.state === 'running' ? 'text-emerald-300' : 'text-slate-300'} />
            </div>
            <div className="grid gap-4 md:grid-cols-2">
              <Metric label="下载流量" value={formatBytes(totals.bytesIn)} />
              <Metric label="上传流量" value={formatBytes(totals.bytesOut)} />
            </div>
            <div className="grid gap-6 xl:grid-cols-2">
              <TunnelList title="VPN 连接" emptyTitle="暂无 VPN" emptyDescription="导入订阅或手动新增 VPN 后会显示在这里。" tunnels={vpnTunnels} onEdit={startEdit} onAction={runTunnelAction} onRequestAdmin={requestAdminRestart} isAdmin={isAdmin} busy={Boolean(busy)} />
              <TunnelList title="SSH 隧道" emptyTitle="暂无 SSH 隧道" emptyDescription="创建本地转发、远程转发或 SOCKS 代理后会显示在这里。" tunnels={sshTunnels} onEdit={startEdit} onAction={runTunnelAction} />
            </div>
          </div>
        )
      case 'vpn':
        return (
          <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_420px]">
            <TunnelList title="VPN 连接" emptyTitle="暂无 VPN" emptyDescription="可以从订阅导入，也可以手动添加 SafeLink VPN 服务端。" tunnels={vpnTunnels} onEdit={startEdit} onAction={runTunnelAction} onRequestAdmin={requestAdminRestart} isAdmin={isAdmin} busy={Boolean(busy)} />
            {renderTunnelFormPanel(['vpn'], editingName ? `编辑 VPN：${editingName}` : '新增 VPN')}
          </div>
        )
      case 'proxy':
        return renderProxyPanel()
      case 'ssh':
        return (
          <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_420px]">
            <TunnelList title="SSH 隧道" emptyTitle="暂无 SSH 隧道" emptyDescription="支持本地转发、远程转发和 SOCKS 代理。" tunnels={sshTunnels} onEdit={startEdit} onAction={runTunnelAction} />
            {renderTunnelFormPanel(sshModes, editingName ? `编辑 SSH 隧道：${editingName}` : '新增 SSH 隧道')}
          </div>
        )
      case 'sshTerminal':
        return renderSSHTerminalPanel()
      case 'subscriptions':
        return renderSubscriptionsPanel()
      case 'driver':
        return renderDriverPanel()
      case 'settings':
        return renderSettingsPanel()
      default:
        return null
    }
  }

  async function runTunnelAction(label: string, item: manager.Status, action: 'start' | 'stop' | 'restart' | 'delete' | 'route-on' | 'route-off') {
    await runAction(label, async () => {
      if (action === 'start') {
        await StartTunnel(item.config.name)
      }
      if (action === 'stop') {
        await StopTunnel(item.config.name)
      }
      if (action === 'restart') {
        await RestartTunnel(item.config.name)
      }
      if (action === 'delete') {
        if (!window.confirm(`确定删除隧道 "${item.config.name}" 吗？`)) {
          return
        }
        await DeleteTunnel(item.config.name)
      }
      if (action === 'route-on') {
        await ToggleRoute(item.config.name, true)
      }
      if (action === 'route-off') {
        await ToggleRoute(item.config.name, false)
      }
    })
  }

  async function runProxyAction(label: string, node?: proxysubscription.ProxyNode) {
    await runAction(label, async () => {
      if (node) {
        await StartProxyNode(node.name)
      } else {
        await StopProxy()
      }
    })
  }

  function renderTunnelFormPanel(modes: TunnelMode[], title: string) {
    const isVPN = form.mode === 'vpn'
    return (
      <Panel title={title}>
        <form className="space-y-4" onSubmit={submitTunnel}>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="名称">
              <input className="input" value={form.name} onChange={(event) => updateForm('name', event.target.value)} required disabled={Boolean(editingName)} />
            </Field>
            <Field label="模式">
              <select className="input" value={form.mode} onChange={(event) => updateForm('mode', event.target.value as TunnelMode)}>
                {modes.map((mode) => (
                  <option key={mode} value={mode}>
                    {modeLabels[mode]}
                  </option>
                ))}
              </select>
            </Field>
          </div>

          {isVPN ? (
            <>
              <Field label="VPN 服务端地址">
                <input className="input" placeholder="vpn.example.com:1562" value={form.forward} onChange={(event) => updateForm('forward', event.target.value)} required />
              </Field>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="认证账户">
                  <input className="input" value={form.sshUser} onChange={(event) => updateForm('sshUser', event.target.value)} required />
                </Field>
                <Field label="认证密码 / Token">
                  <input className="input" type="password" value={form.password} onChange={(event) => updateForm('password', event.target.value)} required />
                </Field>
              </div>
            </>
          ) : (
            <>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="SSH 地址">
                  <input className="input" placeholder="example.com:22" value={form.sshAddr} onChange={(event) => updateForm('sshAddr', event.target.value)} required />
                </Field>
                <Field label="SSH 用户">
                  <input className="input" value={form.sshUser} onChange={(event) => updateForm('sshUser', event.target.value)} required />
                </Field>
              </div>

              <Field label="密码">
                <input className="input" type="password" value={form.password} onChange={(event) => updateForm('password', event.target.value)} required />
              </Field>

              <div className="grid gap-3 sm:grid-cols-2">
                <Field label={form.mode === 'remote' ? '远端监听' : '本地监听'}>
                  <input className="input" placeholder="127.0.0.1:1080" value={form.listen} onChange={(event) => updateForm('listen', event.target.value)} />
                </Field>
                {form.mode !== 'dynamic' && (
                  <Field label="转发目标">
                    <input className="input" placeholder="127.0.0.1:80" value={form.forward} onChange={(event) => updateForm('forward', event.target.value)} required />
                  </Field>
                )}
              </div>

              <Field label="传输层">
                <select className="input" value={form.transport} onChange={(event) => updateForm('transport', event.target.value)}>
                  <option value="tcp">TCP</option>
                  <option value="quic">QUIC</option>
                </select>
              </Field>
            </>
          )}

          {isVPN && (
            <div className="space-y-4 rounded-xl border border-slate-700 bg-slate-950/60 p-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="TUN 子网">
                  <input className="input" placeholder="10.8.0.2/24" value={form.subnet} onChange={(event) => updateForm('subnet', event.target.value)} required />
                </Field>
                <Field label="DNS">
                  <input className="input" placeholder="1.1.1.1,8.8.8.8" value={form.dns} onChange={(event) => updateForm('dns', event.target.value)} />
                </Field>
              </div>
              <label className="flex items-center gap-2 text-sm text-slate-300">
                <input checked={form.autoRoute} onChange={(event) => updateForm('autoRoute', event.target.checked)} type="checkbox" />
                启动后自动接管系统路由
              </label>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="TLS 证书">
                  <input className="input" value={form.tlsCert} onChange={(event) => updateForm('tlsCert', event.target.value)} />
                </Field>
                <Field label="TLS 私钥">
                  <input className="input" value={form.tlsKey} onChange={(event) => updateForm('tlsKey', event.target.value)} />
                </Field>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="SNI">
                  <input className="input" value={form.sni} onChange={(event) => updateForm('sni', event.target.value)} />
                </Field>
                <Field label="证书 Pin SHA256">
                  <input className="input" value={form.pinSHA256} onChange={(event) => updateForm('pinSHA256', event.target.value)} />
                </Field>
              </div>
              <Field label="填充">
                <select className="input" value={form.padding} onChange={(event) => updateForm('padding', event.target.value as TunnelForm['padding'])}>
                  <option value="default">默认</option>
                  <option value="enabled">启用</option>
                  <option value="disabled">禁用</option>
                </select>
              </Field>
            </div>
          )}

          <div className="flex gap-2">
            <button className="btn-primary" disabled={Boolean(busy)} type="submit">
              {editingName ? '保存并重启' : '新增并启动'}
            </button>
            {editingName && (
              <button
                className="btn-secondary"
                onClick={() => {
                  setEditingName('')
                  setForm(emptyForm)
                }}
                type="button"
              >
                取消编辑
              </button>
            )}
          </div>
        </form>
      </Panel>
    )
  }

  function renderSubscriptionsPanel() {
    return (
      <div className="grid gap-6 xl:grid-cols-[420px_minmax(0,1fr)]">
        <Panel title="导入订阅">
          <form className="space-y-4" onSubmit={submitSubscription}>
            <Field label="名称">
              <input className="input" value={subName} onChange={(event) => setSubName(event.target.value)} required />
            </Field>
            <Field label="订阅 URL">
              <input className="input" value={subUrl} onChange={(event) => setSubUrl(event.target.value)} required />
            </Field>
            <button className="btn-primary" disabled={Boolean(busy)} type="submit">
              导入
            </button>
          </form>
          <p className="mt-4 text-xs text-slate-500">支持 SafeLink VPN 订阅、Clash 机场订阅、V2Ray/base64 URI 列表、单节点 URI 和 sing-box JSON。</p>
        </Panel>

        <Panel title="订阅源">
          {subscriptions.length === 0 ? (
            <EmptyState title="暂无订阅" description="添加机场或服务端订阅地址后会显示在这里。" />
          ) : (
            <div className="space-y-3">
              {subscriptions.map((item) => (
                <div className="rounded-xl border border-slate-800 bg-slate-900 p-4" key={item.id}>
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <p className="font-medium">{item.name}</p>
                      <p className="mt-1 break-all text-sm text-slate-400">{item.url}</p>
                      <p className="mt-2 text-xs text-slate-500">
                        类型：{subscriptionKindLabel(item.kind)} · 格式：{item.format || 'auto'} · 隧道：{item.tunnel_count || 0} · 代理节点：{item.node_count || 0}
                      </p>
                    </div>
                    <button className="btn-danger" onClick={() => runAction('删除订阅', () => DeleteSubscription(item.id))} type="button">
                      删除
                    </button>
                  </div>
                  {item.last_error && <p className="mt-3 text-sm text-rose-300">{item.last_error}</p>}
                </div>
              ))}
            </div>
          )}
        </Panel>
      </div>
    )
  }

  function renderProxyPanel() {
    const running = proxyStatus?.state === 'running'
    return (
      <div className="space-y-6">
        <Panel title="代理核心">
          <div className="grid gap-3 text-sm md:grid-cols-4">
            <InfoRow label="状态" value={running ? '运行中' : proxyStatus?.state || 'stopped'} valueClass={running ? 'text-emerald-300' : 'text-slate-300'} />
            <InfoRow label="当前节点" value={proxyStatus?.node_name || '-'} />
            <InfoRow label="SOCKS5" value={proxyStatus?.socks_addr || '127.0.0.1:10808'} />
            <InfoRow label="HTTP" value={proxyStatus?.http_addr || '127.0.0.1:10809'} />
          </div>
          <div className="mt-4 flex flex-wrap gap-2">
            <button className="btn-secondary" onClick={() => runProxyAction('停止代理')} type="button" disabled={!running || Boolean(busy)}>
              停止代理
            </button>
          </div>
          {proxyStatus?.last_error && <p className="mt-3 text-sm text-rose-300">{proxyStatus.last_error}</p>}
        </Panel>

        <Panel title="代理节点">
          {proxyNodes.length === 0 ? (
            <EmptyState title="暂无代理节点" description="在订阅页导入机场或主流协议订阅后会显示在这里。" />
          ) : (
            <div className="space-y-3">
              {proxyNodes.map((node) => (
                <div className="rounded-xl border border-slate-800 bg-slate-900 p-4" key={node.id || node.name}>
                  <div className="flex flex-wrap items-start justify-between gap-4">
                    <div>
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-lg font-semibold">{node.name}</h3>
                        <span className="rounded-full bg-slate-800 px-2 py-1 text-xs text-slate-300">{node.protocol}</span>
                        {proxyStatus?.node_name === node.name && running && <span className="rounded-full bg-emerald-500/15 px-2 py-1 text-xs text-emerald-300">当前</span>}
                      </div>
                      <p className="mt-2 text-sm text-slate-400">{node.server}:{node.port}</p>
                      <p className="mt-1 text-xs text-slate-500">
                        {node.tls?.enabled ? `TLS ${node.tls.server_name || ''}` : '无 TLS'} {node.transport?.type ? `· ${node.transport.type}` : ''}
                      </p>
                    </div>
                    <button className="btn-primary" onClick={() => runProxyAction('启动代理', node)} type="button" disabled={Boolean(busy)}>
                      启动
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Panel>
      </div>
    )
  }

  function renderSSHTerminalPanel() {
    return (
      <div className="grid gap-6 xl:grid-cols-[360px_minmax(0,1fr)]">
        <Panel title="SSH 连接">
          <div className="mb-4 flex flex-wrap gap-2">
            <button
              className="btn-primary"
              onClick={() => {
                setTerminalForm(emptyTerminalForm)
                setShowTerminalForm(true)
              }}
              type="button"
            >
              新增连接
            </button>
          </div>

          {showTerminalForm && (
            <form className="mb-5 space-y-4 rounded-xl border border-slate-800 bg-slate-950/60 p-4" onSubmit={saveTerminalConnection}>
              <Field label="连接名称">
                <input className="input" placeholder="生产服务器" value={terminalForm.name} onChange={(event) => updateTerminalForm('name', event.target.value)} required />
              </Field>
              <Field label="SSH 地址">
                <input className="input" placeholder="example.com:22" value={terminalForm.addr} onChange={(event) => updateTerminalForm('addr', event.target.value)} required />
              </Field>
              <Field label="用户名">
                <input className="input" value={terminalForm.user} onChange={(event) => updateTerminalForm('user', event.target.value)} required />
              </Field>
              <Field label="密码">
                <input className="input" type="password" value={terminalForm.password} onChange={(event) => updateTerminalForm('password', event.target.value)} required />
              </Field>
              <div className="flex flex-wrap gap-2">
                <button className="btn-primary" disabled={Boolean(busy)} type="submit">
                  保存
                </button>
                <button className="btn-secondary" onClick={() => setShowTerminalForm(false)} type="button">
                  取消
                </button>
              </div>
            </form>
          )}

          {sshConnections.length === 0 ? (
            <EmptyState title="暂无 SSH 连接" description="点击新增连接保存账号密码 SSH 配置。" />
          ) : (
            <div className="space-y-3">
              {sshConnections.map((conn) => (
                <div className="rounded-xl border border-slate-800 bg-slate-900 p-4" key={conn.id}>
                  <div>
                    <p className="font-medium">{conn.name}</p>
                    <p className="mt-1 break-all text-sm text-slate-400">
                      {conn.user}@{conn.addr}
                    </p>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <button className="btn-primary" disabled={Boolean(busy)} onClick={() => openTerminalWindow(conn)} type="button">
                      连接
                    </button>
                    <button
                      className="btn-secondary"
                      onClick={() => {
                        setTerminalForm({ id: conn.id, name: conn.name, addr: conn.addr, user: conn.user, password: conn.password, identityFile: '', passphrase: '' })
                        setShowTerminalForm(true)
                      }}
                      type="button"
                    >
                      编辑
                    </button>
                    <button className="btn-danger" onClick={() => deleteSSHConnection(conn)} type="button">
                      删除
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Panel>

        <Panel title="实时终端">
          {terminalWindows.length === 0 ? (
            <EmptyState title="暂无终端窗口" description="从左侧已保存连接点击连接后，会在这里打开实时 SSH 终端。" />
          ) : (
            <div className="space-y-3">
              <div className="flex flex-wrap gap-2">
                {terminalWindows.map((win) => {
                  const active = activeTerminalWindowID === win.localId
                  return (
                    <div
                      className={`inline-flex max-w-full items-center overflow-hidden rounded-lg text-sm transition ${active ? 'bg-cyan-500 text-slate-950' : 'bg-slate-800 text-slate-200'}`}
                      key={win.localId}
                    >
                      <button
                        className={`flex max-w-[260px] items-center gap-2 truncate py-2 pl-2 pr-1 text-left ${active ? '' : 'hover:bg-slate-700'}`}
                        onClick={() => setActiveTerminalWindowID(win.localId)}
                        onContextMenu={(event) => {
                          event.preventDefault()
                          event.stopPropagation()
                          setActiveTerminalWindowID(win.localId)
                          setTerminalTabMenu({ x: event.clientX, y: event.clientY, win })
                        }}
                        title={win.title}
                        type="button"
                      >
                        <span
                          className={`h-2 w-2 shrink-0 rounded-full ${isTerminalWindowConnected(win.status) ? 'bg-emerald-400' : 'bg-rose-500'}`}
                          title={win.status}
                        />
                        <span className="truncate">{win.title}</span>
                      </button>
                      <button
                        aria-label="关闭窗口"
                        className={`mx-1.5 my-1 flex h-5 w-5 shrink-0 items-center justify-center self-center rounded-full text-sm leading-none transition ${
                          active
                            ? 'text-slate-950 hover:bg-slate-950/15 hover:ring-1 hover:ring-slate-950/30'
                            : 'text-slate-400 hover:bg-rose-500/15 hover:text-rose-300 hover:ring-1 hover:ring-rose-500/40'
                        }`}
                        onClick={() => {
                          setTerminalTabMenu(null)
                          closeTerminalWindow(win)
                        }}
                        title="关闭窗口"
                        type="button"
                      >
                        ×
                      </button>
                    </div>
                  )
                })}
              </div>

              {terminalTabMenu && (
                <div
                  className="fixed z-50 min-w-[140px] overflow-hidden rounded-lg border border-slate-700 bg-slate-900 py-1 shadow-xl"
                  style={{ left: terminalTabMenu.x, top: terminalTabMenu.y }}
                  onClick={(event) => event.stopPropagation()}
                >
                  <button
                    className="block w-full px-4 py-2 text-left text-sm text-slate-200 hover:bg-slate-800"
                    onClick={() => {
                      openTerminalWindow(terminalTabMenu.win.connection)
                      setTerminalTabMenu(null)
                    }}
                    type="button"
                  >
                    复制窗口
                  </button>
                  <button
                    className="block w-full px-4 py-2 text-left text-sm text-slate-200 hover:bg-slate-800"
                    onClick={() => {
                      reconnectTerminalWindow(terminalTabMenu.win)
                      setTerminalTabMenu(null)
                    }}
                    type="button"
                  >
                    重连
                  </button>
                </div>
              )}

              <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-950 p-2">
                {terminalWindows.map((win) => (
                  <SSHTerminalPane
                    active={activeTerminalWindowID === win.localId}
                    key={win.localId}
                    sessionID={win.sessionId}
                    title={win.title}
                    onClosed={(message) => {
                      setTerminalWindows((current) => current.map((item) => (item.localId === win.localId ? { ...item, status: message ? `已断开：${message}` : '已断开' } : item)))
                    }}
                    onError={(message) => {
                      setTerminalWindows((current) => current.map((item) => (item.localId === win.localId ? { ...item, status: `错误：${message}` } : item)))
                    }}
                  />
                ))}
              </div>
            </div>
          )}
        </Panel>
      </div>
    )
  }

  function renderDriverPanel() {
    return (
      <Panel title="TUN 驱动">
        <div className="flex flex-wrap gap-2">
          <button className="btn-primary" onClick={checkDriver} type="button" disabled={Boolean(busy)}>
            检测驱动
          </button>
          <button className="btn-secondary" onClick={installDriver} type="button" disabled={Boolean(busy)}>
            自动安装
          </button>
        </div>
        {driverStatus ? (
          <div className="mt-5 grid gap-3 text-sm">
            <InfoRow label="系统" value={driverStatus.os} />
            <InfoRow label="安装状态" value={driverStatus.installed ? '已安装' : '未安装'} valueClass={driverStatus.installed ? 'text-emerald-300' : 'text-amber-300'} />
            <InfoRow label="管理员权限" value={driverStatus.is_admin ? '已获取' : '未获取'} valueClass={driverStatus.is_admin ? 'text-emerald-300' : 'text-amber-300'} />
            <InfoRow label="驱动路径" value={driverStatus.driver_path || '-'} />
            <InfoRow label="可自动修复" value={driverStatus.can_auto_fix ? '是' : '否'} />
            <InfoRow label="说明" value={driverStatus.message || '-'} />
            {driverStatus.can_request_admin && (
              <AdminElevationPrompt onRequestAdmin={requestAdminRestart} busy={Boolean(busy)} compact />
            )}
          </div>
        ) : (
          <EmptyState title="尚未检测" description="VPN 模式需要 WinTUN 驱动，点击检测查看本机状态。" />
        )}
      </Panel>
    )
  }

  function renderSettingsPanel() {
    return (
      <Panel title="客户端设置">
        <div className="grid gap-3 text-sm">
          <InfoRow label="版本" value={version} />
          <InfoRow label="数据目录" value={dataDir || '-'} />
          <InfoRow label="刷新间隔" value="3 秒" />
          <InfoRow label="Wails 后端" value={backendReady ? '已连接' : '未连接'} valueClass={backendReady ? 'text-emerald-300' : 'text-amber-300'} />
        </div>
      </Panel>
    )
  }

  function updateForm<K extends keyof TunnelForm>(key: K, value: TunnelForm[K]) {
    setForm((current) => ({ ...current, [key]: value }))
  }

  function updateTerminalForm<K extends keyof SSHTerminalForm>(key: K, value: SSHTerminalForm[K]) {
    setTerminalForm((current) => ({ ...current, [key]: value }))
  }
}

function TunnelList({
  title = '隧道列表',
  emptyTitle = '暂无隧道',
  emptyDescription = '创建 local、remote、dynamic 或 VPN 隧道后，就可以在这里启动、停止和查看流量。',
  tunnels,
  onEdit,
  onAction,
  onRequestAdmin,
  isAdmin = true,
  busy = false,
}: {
  title?: string
  emptyTitle?: string
  emptyDescription?: string
  tunnels: manager.Status[]
  onEdit: (item: manager.Status) => void
  onAction: (label: string, item: manager.Status, action: 'start' | 'stop' | 'restart' | 'delete' | 'route-on' | 'route-off') => void
  onRequestAdmin?: () => void
  isAdmin?: boolean
  busy?: boolean
}) {
  if (tunnels.length === 0) {
    return (
      <Panel title={title}>
        <EmptyState title={emptyTitle} description={emptyDescription} />
      </Panel>
    )
  }

  return (
    <Panel title={title}>
      <div className="space-y-3">
        {tunnels.map((item) => (
          <div className="rounded-xl border border-slate-800 bg-slate-900 p-4" key={item.config.name}>
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="text-lg font-semibold">{item.config.name}</h3>
                  <StateBadge state={item.state} />
                  <span className="rounded-full bg-slate-800 px-2 py-1 text-xs text-slate-300">{modeLabels[normalizeMode(item.config.mode)]}</span>
                </div>
                <p className="mt-2 text-sm text-slate-400">{tunnelEndpointLabel(item.config)}</p>
                <p className="mt-1 text-sm text-slate-500">
                  {item.config.listen || '-'} {item.config.forward ? `-> ${item.config.forward}` : ''}
                </p>
                {item.last_error && <p className="mt-2 text-sm text-rose-300">{item.last_error}</p>}
                {item.config.mode === 'vpn' && !isAdmin && item.last_error && isTunAccessDenied(item.last_error) && onRequestAdmin && (
                  <AdminElevationPrompt onRequestAdmin={onRequestAdmin} busy={busy} />
                )}
              </div>

              <div className="flex flex-wrap justify-end gap-2">
                <button className="btn-secondary" onClick={() => onEdit(item)} type="button">
                  编辑
                </button>
                <button className="btn-secondary" onClick={() => onAction('启动隧道', item, 'start')} type="button">
                  启动
                </button>
                <button className="btn-secondary" onClick={() => onAction('停止隧道', item, 'stop')} type="button">
                  停止
                </button>
                <button className="btn-secondary" onClick={() => onAction('重启隧道', item, 'restart')} type="button">
                  重启
                </button>
                {item.config.mode === 'vpn' && (
                  <button className="btn-secondary" onClick={() => onAction(item.route_active ? '关闭路由' : '开启路由', item, item.route_active ? 'route-off' : 'route-on')} type="button">
                    {item.route_active ? '关闭路由' : '开启路由'}
                  </button>
                )}
                <button className="btn-danger" onClick={() => onAction('删除隧道', item, 'delete')} type="button">
                  删除
                </button>
              </div>
            </div>

            <div className="mt-4 grid gap-3 text-sm md:grid-cols-4">
              <InfoRow label="运行次数" value={item.run_count} />
              <InfoRow label="运行时长" value={formatDuration(item.uptime_seconds)} />
              <InfoRow label="入站" value={formatBytes(item.stats?.bytes_in ?? 0)} />
              <InfoRow label="出站" value={formatBytes(item.stats?.bytes_out ?? 0)} />
            </div>
          </div>
        ))}
      </div>
    </Panel>
  )
}

function AdminElevationPrompt({ onRequestAdmin, busy, compact = false }: { onRequestAdmin: () => void; busy: boolean; compact?: boolean }) {
  return (
    <div className={`${compact ? 'mt-3' : 'mt-3'} rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm text-amber-100`}>
      <p>创建 TUN 虚拟网卡需要管理员权限。是否以管理员身份重新启动 SafeLink？</p>
      <button className="btn-primary mt-3" type="button" disabled={busy} onClick={onRequestAdmin}>
        以管理员身份重启
      </button>
    </div>
  )
}

function isTunAccessDenied(message?: string) {
  if (!message) {
    return false
  }
  const lower = message.toLowerCase()
  return lower.includes('access is denied') || lower.includes('access denied') || message.includes('拒绝访问')
}

function Panel({ title, children }: { title?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-2xl border border-slate-800 bg-slate-900/70 p-5 shadow-xl shadow-black/10">
      {title && <h3 className="mb-4 text-lg font-semibold">{title}</h3>}
      {children}
    </section>
  )
}

function Metric({ label, value, accent = 'text-white' }: { label: string; value: string | number; accent?: string }) {
  return (
    <Panel>
      <p className="text-sm text-slate-400">{label}</p>
      <p className={`mt-2 text-3xl font-semibold ${accent}`}>{value}</p>
    </Panel>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm text-slate-300">{label}</span>
      {children}
    </label>
  )
}

function InfoRow({ label, value, valueClass = 'text-slate-200' }: { label: string; value: string | number; valueClass?: string }) {
  return (
    <div className="rounded-lg bg-slate-950/70 p-3">
      <p className="text-xs text-slate-500">{label}</p>
      <p className={`mt-1 break-all ${valueClass}`}>{value}</p>
    </div>
  )
}

function EmptyState({ title, description }: { title: string; description: string }) {
  return (
    <div className="rounded-xl border border-dashed border-slate-700 p-8 text-center">
      <p className="font-medium text-slate-200">{title}</p>
      <p className="mt-2 text-sm text-slate-500">{description}</p>
    </div>
  )
}

function StateBadge({ state }: { state: string }) {
  const color =
    state === 'running'
      ? 'bg-emerald-500/15 text-emerald-300'
      : state === 'connecting'
        ? 'bg-amber-500/15 text-amber-300'
        : state === 'stopped'
          ? 'bg-slate-700 text-slate-300'
          : 'bg-rose-500/15 text-rose-300'
  return <span className={`rounded-full px-2 py-1 text-xs ${color}`}>{state || 'unknown'}</span>
}

function pageDescription(tab: TabKey) {
  switch (tab) {
    case 'vpn':
      return '导入订阅或手动配置 VPN 服务端，管理连接状态和系统路由。'
    case 'proxy':
      return '使用 sing-box 运行机场或主流协议节点，提供本地 SOCKS5/HTTP 代理。'
    case 'ssh':
      return '单独配置本地转发、远程转发和 SOCKS 代理。'
    case 'sshTerminal':
      return '打开交互式 SSH PTY 终端，实时输入命令并查看输出。'
    case 'subscriptions':
      return '从 SafeLink、Clash、V2Ray/base64 URI 或 sing-box 订阅导入节点。'
    case 'driver':
      return '检测和安装 VPN 所需的本机 TUN 驱动。'
    case 'settings':
      return '查看客户端版本、数据目录和运行状态。'
    default:
      return '查看 VPN、SSH 隧道、订阅和本机驱动的整体状态。'
  }
}

function subscriptionKindLabel(kind?: string) {
  if (kind === 'proxy') {
    return '代理'
  }
  return 'VPN'
}

function tunnelEndpointLabel(cfg: config.TunnelCfg) {
  if (normalizeMode(cfg.mode) === 'vpn') {
    return `VPN ${cfg.forward || '-'}`
  }
  return `SSH ${cfg.ssh?.user || '-'}@${cfg.ssh?.addr || '-'}`
}

function buildTunnelConfig(form: TunnelForm): config.TunnelCfg {
  const padding = form.padding === 'default' ? undefined : form.padding === 'enabled'
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
    transport: form.transport,
    tun: {
      subnet: form.subnet.trim(),
      dns: form.dns
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean),
      auto_route: form.autoRoute,
      tls_cert: form.tlsCert.trim(),
      tls_key: form.tlsKey.trim(),
      sni: form.sni.trim(),
      pin_sha256: form.pinSHA256.trim(),
      padding,
    },
  } as config.TunnelCfg
}

function normalizeMode(mode: string): TunnelMode {
  if (mode === 'remote' || mode === 'dynamic' || mode === 'vpn') {
    return mode
  }
  return 'local'
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
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`
}

function formatDuration(seconds: number) {
  if (!seconds) {
    return '-'
  }
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60
  if (hours > 0) {
    return `${hours}h ${minutes}m`
  }
  if (minutes > 0) {
    return `${minutes}m ${rest}s`
  }
  return `${rest}s`
}

function upsertSSHConnection(current: store.SSHConnection[], next: store.SSHConnection) {
  const found = current.some((item) => item.id === next.id)
  if (!found) {
    return [...current, next]
  }
  return current.map((item) => (item.id === next.id ? next : item))
}

function newLocalID() {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function SSHTerminalPane({
  active,
  sessionID,
  title,
  onClosed,
  onError,
}: {
  active: boolean
  sessionID: string
  title: string
  onClosed: (message?: string) => void
  onError: (message: string) => void
}) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const activeRef = useRef(active)
  const onClosedRef = useRef(onClosed)
  const onErrorRef = useRef(onError)

  useEffect(() => {
    activeRef.current = active
  }, [active])

  useEffect(() => {
    onClosedRef.current = onClosed
    onErrorRef.current = onError
  }, [onClosed, onError])

  useEffect(() => {
    if (!containerRef.current || terminalRef.current) {
      return
    }
    const term = new Terminal({
      cursorBlink: true,
      fontFamily: 'Consolas, "Cascadia Mono", "Courier New", monospace',
      fontSize: 13,
      scrollback: 5000,
      theme: {
        background: '#020617',
        foreground: '#e2e8f0',
        cursor: '#22d3ee',
        selectionBackground: '#334155',
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    terminalRef.current = term
    fitAddonRef.current = fit
    term.writeln(`SafeLink SSH Terminal - ${title}`)

    const dataDisposable = term.onData((data) => {
      SendSSHInput(sessionID, data).catch((err) => {
        const message = err instanceof Error ? err.message : String(err)
        onErrorRef.current(message)
      })
    })
    const offOutput = EventsOn('ssh:output', (event: SSHOutputEvent) => {
      if (event?.session_id === sessionID) {
        term.write(event.data)
      }
    })
    const offClosed = EventsOn('ssh:closed', (event: SSHClosedEvent) => {
      if (event?.session_id === sessionID) {
        onClosedRef.current(event.message)
        term.writeln('\r\n[连接已关闭]')
      }
    })
    const offError = EventsOn('ssh:error', (event: SSHErrorEvent) => {
      if (event?.session_id === sessionID) {
        onErrorRef.current(event.message)
        term.writeln(`\r\n[错误] ${event.message}`)
      }
    })
    const resize = () => {
      if (!activeRef.current) {
        return
      }
      fit.fit()
      ResizeSSHSession(sessionID, term.rows, term.cols).catch(() => undefined)
    }
    window.addEventListener('resize', resize)

    return () => {
      dataDisposable.dispose()
      offOutput()
      offClosed()
      offError()
      window.removeEventListener('resize', resize)
      term.dispose()
      terminalRef.current = null
      fitAddonRef.current = null
    }
  }, [sessionID, title])

  useEffect(() => {
    if (!active || !terminalRef.current || !fitAddonRef.current) {
      return
    }
    window.setTimeout(() => {
      fitAddonRef.current?.fit()
      const term = terminalRef.current
      if (term) {
        ResizeSSHSession(sessionID, term.rows, term.cols).catch(() => undefined)
        term.focus()
      }
    }, 0)
  }, [active, sessionID])

  return (
    <div className={active ? 'block' : 'hidden'} onPointerDown={() => terminalRef.current?.focus()}>
      <div className="h-[560px]" ref={containerRef} />
    </div>
  )
}

export default App
