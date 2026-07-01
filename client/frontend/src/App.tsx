import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import type { FormEvent, MouseEvent as ReactMouseEvent } from 'react'
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
  GetClientSettings,
  GetLaunchOptions,
  GetLogs,
  GetMachineInfo,
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
  RecordSSHDebug,
  SaveSSHConnection,
  SendSSHInputBase64Batch,
  SetAutoStartEnabled,
  SetProxyMode,
  SetProxyRules,
  SetSystemProxyEnabled,
  StartProxyNode,
  StartTunnel,
  StopProxy,
  StopTunnel,
  TestProxyNodes,
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
  state: string
  lastError: string
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

type MachineInfo = {
  hostname?: string
  username?: string
  os?: string
  arch?: string
  cpu_cores?: number
  ips?: string[]
}

const TUNNEL_POLL_INTERVAL_MS = 350
const TUNNEL_START_TIMEOUT_MS = 20000
const TUNNEL_STOP_TIMEOUT_MS = 8000
const SSH_TERMINAL_ENABLED = true

type ProxyTestProgressEvent = {
  batch_id?: string
  result?: proxycore.TestResult
  completed?: number
  total?: number
}

type ModalKind = 'subscription' | 'tunnel' | 'ssh' | null

type ProxyMode = 'rule' | 'global' | 'direct'
type SettingsTab = 'general' | 'routing'
type ProxyRuleType = 'domain' | 'domain_suffix' | 'domain_keyword' | 'ip_cidr' | 'rule_set'
type ProxyRuleOutbound = 'selected' | 'direct' | 'block'
type TunnelPendingAction = 'starting' | 'stopping'
type LogLevelFilter = 'all' | 'info' | 'warn' | 'error'
type LogRangeFilter = 'all' | '1h' | '24h' | '7d'

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
  data_base64?: string
}

type SSHClosedEvent = {
  session_id: string
  message?: string
}

type TerminalTab = {
  id: string
  sessionID: string
  connectionID: string
  title: string
  host: string
  addr: string
  user: string
  password: string
  connected: boolean
}

type TerminalTabMenu = {
  tabID: string
  x: number
  y: number
}

const navItems: Array<{ key: NavKey; label: string; icon: string; badge?: string }> = [
  { key: 'home', label: '首页', icon: 'home' },
  { key: 'nodes', label: '节点', icon: 'node' },
  { key: 'subscriptions', label: '订阅', icon: 'sub' },
  { key: 'tunnels', label: 'SSH 隧道', icon: 'tunnel' },
  ...(SSH_TERMINAL_ENABLED ? [{ key: 'terminal' as const, label: 'SSH 终端', icon: 'term' }] : []),
  { key: 'settings', label: '设置', icon: 'gear' },
  { key: 'logs', label: '日志', icon: 'log' },
]

const logoSrc = new URL('./static/logo.png', import.meta.url).href

const settingsTabs: Array<{ key: SettingsTab; label: string }> = [
  { key: 'general', label: '常规设置' },
  { key: 'routing', label: '路由设置' },
]

const defaultRouteRules: config.ProxyRule[] = [
  { id: 'lan-ipv4-this-network', name: 'IPv4 保留地址', type: 'ip_cidr', value: '0.0.0.0/8', outbound: 'direct', enabled: true },
  { id: 'lan-ipv4-private-10', name: 'IPv4 私有地址', type: 'ip_cidr', value: '10.0.0.0/8', outbound: 'direct', enabled: true },
  { id: 'lan-ipv4-carrier', name: '运营商内网', type: 'ip_cidr', value: '100.64.0.0/10', outbound: 'direct', enabled: true },
  { id: 'lan-ipv4-loopback', name: '本机地址', type: 'ip_cidr', value: '127.0.0.0/8', outbound: 'direct', enabled: true },
  { id: 'lan-ipv4-link-local', name: '链路本地', type: 'ip_cidr', value: '169.254.0.0/16', outbound: 'direct', enabled: true },
  { id: 'lan-ipv4-private-172', name: 'IPv4 私有地址', type: 'ip_cidr', value: '172.16.0.0/12', outbound: 'direct', enabled: true },
  { id: 'lan-ipv4-private-192', name: 'IPv4 私有地址', type: 'ip_cidr', value: '192.168.0.0/16', outbound: 'direct', enabled: true },
  { id: 'lan-ipv4-multicast', name: '组播地址', type: 'ip_cidr', value: '224.0.0.0/4', outbound: 'direct', enabled: true },
  { id: 'lan-ipv6-loopback', name: 'IPv6 本机地址', type: 'ip_cidr', value: '::1/128', outbound: 'direct', enabled: true },
  { id: 'lan-ipv6-unique-local', name: 'IPv6 私有地址', type: 'ip_cidr', value: 'fc00::/7', outbound: 'direct', enabled: true },
  { id: 'lan-ipv6-link-local', name: 'IPv6 链路本地', type: 'ip_cidr', value: 'fe80::/10', outbound: 'direct', enabled: true },
  { id: 'foreign-geosite', name: '国外域名', type: 'rule_set', value: 'geosite-geolocation-!cn', outbound: 'selected', enabled: true },
  { id: 'cn-geosite', name: '国内域名', type: 'rule_set', value: 'geosite-geolocation-cn', outbound: 'direct', enabled: true },
  { id: 'cn-geoip', name: '国内 IP', type: 'rule_set', value: 'geoip-cn', outbound: 'direct', enabled: true },
]

const defaultClientSettings: store.ClientSettings = {
  proxy_mode: 'rule',
  system_proxy: false,
  auto_start: false,
  bypass_lan: false,
  auto_connect: false,
  minimize_to_tray: false,
  rule_mode_rules: defaultRouteRules,
  rule_mode_rules_version: 1,
} as store.ClientSettings

const proxyModeOptions: Array<{ value: ProxyMode; label: string }> = [
  { value: 'rule', label: '规则模式' },
  { value: 'global', label: '全局模式' },
  { value: 'direct', label: '直连模式' },
]

const routeRuleTypeOptions: Array<{ value: ProxyRuleType; label: string }> = [
  { value: 'domain_suffix', label: '域名后缀' },
  { value: 'domain', label: '完整域名' },
  { value: 'domain_keyword', label: '域名关键词' },
  { value: 'ip_cidr', label: 'IP CIDR' },
  { value: 'rule_set', label: '规则集' },
]

const routeOutboundOptions: Array<{ value: ProxyRuleOutbound; label: string }> = [
  { value: 'direct', label: '白名单' },
  { value: 'selected', label: '代理' },
  { value: 'block', label: '阻断' },
]

const logLevelOptions: Array<{ value: LogLevelFilter; label: string }> = [
  { value: 'all', label: '全部' },
  { value: 'info', label: '信息' },
  { value: 'warn', label: '警告' },
  { value: 'error', label: '错误' },
]

const logRangeOptions: Array<{ value: LogRangeFilter; label: string }> = [
  { value: 'all', label: '全部' },
  { value: '1h', label: '最近 1 小时' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
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
  listen: '',
  forward: '',
  sshAddr: '',
  sshUser: '',
  password: '',
}

const tunnelModeGuides: Record<TunnelForm['mode'], {
  listenPlaceholder: string
  forwardPlaceholder: string
  listenHelp: string
  forwardHelp: string
}> = {
  local: {
    listenPlaceholder: '例如：127.0.0.1:3307',
    forwardPlaceholder: '例如：127.0.0.1:3306 或 intranet-db:3306',
    listenHelp: '本地转发会在当前电脑监听这个地址，连接会通过 SSH 服务器转发出去。',
    forwardHelp: '目标地址填写 SSH 服务器侧可以访问的服务地址，例如远端内网数据库或服务端本机端口。',
  },
  remote: {
    listenPlaceholder: '例如：0.0.0.0:8080',
    forwardPlaceholder: '例如：127.0.0.1:3000 或 192.168.1.10:80',
    listenHelp: '内网穿透会让 SSH 服务器监听这个地址，公网或服务器侧访问这里会回到你的本机。',
    forwardHelp: '目标地址填写当前电脑或当前内网可访问的服务地址，例如本地开发服务或内网设备。',
  },
  dynamic: {
    listenPlaceholder: '例如：127.0.0.1:1080',
    forwardPlaceholder: '动态转发无需填写',
    listenHelp: '动态转发会在当前电脑开启一个 SOCKS5 代理入口，应用通过这个地址按需访问目标站点。',
    forwardHelp: '动态转发没有固定目标地址，最终访问目标由浏览器或代理客户端的请求决定。',
  },
}

const emptySSHForm: SSHForm = {
  id: '',
  name: '',
  addr: '',
  user: '',
  password: '',
}

const sshAddrPlaceholder = '例如：159.75.35.104:22'

function App() {
  const [activeNav, setActiveNav] = useState<NavKey>('home')
  const [version, setVersion] = useState('2.0.0')
  const [machineInfo, setMachineInfo] = useState<MachineInfo>(() => browserMachineInfo())
  const [tunnels, setTunnels] = useState<manager.Status[]>([])
  const [subscriptions, setSubscriptions] = useState<store.SubscriptionSource[]>([])
  const [proxyNodes, setProxyNodes] = useState<proxysubscription.ProxyNode[]>([])
  const [proxyStatus, setProxyStatus] = useState<proxycore.Status | null>(null)
  const [clientSettings, setClientSettings] = useState<store.ClientSettings>(defaultClientSettings)
  const [activeSettingsTab, setActiveSettingsTab] = useState<SettingsTab>('general')
  const [routeRules, setRouteRules] = useState<config.ProxyRule[]>(() => cloneRouteRules(defaultRouteRules))
  const [routeRulesDirty, setRouteRulesDirty] = useState(false)
  const [sshConnections, setSSHConnections] = useState<store.SSHConnection[]>([])
  const [logs, setLogs] = useState<main.LogEntry[]>([])
  const [proxyTestResults, setProxyTestResults] = useState<Record<string, proxycore.TestResult>>({})
  const [testingNodeNames, setTestingNodeNames] = useState<Set<string>>(() => new Set())
  const [tunnelPending, setTunnelPending] = useState<Record<string, TunnelPendingAction>>({})
  const [selectedNodeID, setSelectedNodeID] = useState('')
  const [selectedTunnelID, setSelectedTunnelID] = useState('')
  const [selectedSSHID, setSelectedSSHID] = useState('')
  const [modal, setModal] = useState<ModalKind>(null)
  const [subscriptionForm, setSubscriptionForm] = useState<SubscriptionForm>(emptySubscriptionForm)
  const [tunnelForm, setTunnelForm] = useState<TunnelForm>(emptyTunnelForm)
  const [sshForm, setSSHForm] = useState<SSHForm>(emptySSHForm)
  const [terminalTabs, setTerminalTabs] = useState<TerminalTab[]>([])
  const [activeTerminalTabID, setActiveTerminalTabID] = useState('')
  const [terminalTabMenu, setTerminalTabMenu] = useState<TerminalTabMenu | null>(null)
  const [terminalConnectionPickerOpen, setTerminalConnectionPickerOpen] = useState(false)
  const [terminalConnectionSearch, setTerminalConnectionSearch] = useState('')
  const [launchSSHConnectionID, setLaunchSSHConnectionID] = useState('')
  const [handledLaunchSSHConnectionID, setHandledLaunchSSHConnectionID] = useState('')
  const [logLevelFilter, setLogLevelFilter] = useState<LogLevelFilter>('all')
  const [logRangeFilter, setLogRangeFilter] = useState<LogRangeFilter>('all')
  const [logSearch, setLogSearch] = useState('')
  const [busy, setBusy] = useState('')
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const routeRulesDirtyRef = useRef(false)

  const backendReady = isBackendReady()

  const refresh = useCallback(async () => {
    if (!isBackendReady()) {
      return
    }

    try {
      const [nextVersion, nextTunnels, nextSubscriptions, nextProxyNodes, nextProxyStatus, nextSettings, nextSSHConnections, nextMachineInfo, nextLogs] = await Promise.all([
        GetVersion(),
        ListTunnels(),
        ListSubscriptions(),
        ListProxyNodes(),
        ProxyStatus(),
        GetClientSettings(),
        ListSSHConnections(),
        GetMachineInfo(),
        GetLogs(),
      ])
      setVersion(nextVersion || '2.0.0')
      setTunnels(nextTunnels ?? [])
      setSubscriptions(nextSubscriptions ?? [])
      setProxyNodes(nextProxyNodes ?? [])
      setProxyStatus(nextProxyStatus ?? null)
      const mergedSettings = mergeClientSettings(nextSettings)
      setClientSettings(mergedSettings)
      if (!routeRulesDirtyRef.current) {
        setRouteRules(normalizeRouteRuleDrafts(mergedSettings.rule_mode_rules))
      }
      setSSHConnections(nextSSHConnections ?? [])
      setMachineInfo(nextMachineInfo ?? browserMachineInfo())
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
    if (!SSH_TERMINAL_ENABLED && activeNav === 'terminal') {
      setActiveNav('home')
    }
  }, [activeNav])

  useEffect(() => {
    if (!backendReady || !SSH_TERMINAL_ENABLED) {
      return
    }
    const offClosed = EventsOn('ssh:closed', (event: SSHClosedEvent) => {
      if (!event?.session_id) {
        return
      }
      setTerminalTabs((current) =>
        current.map((tab) => (tab.sessionID === event.session_id ? { ...tab, connected: false } : tab)),
      )
    })
    return () => offClosed()
  }, [backendReady])

  useEffect(() => {
    if (!backendReady || !SSH_TERMINAL_ENABLED) {
      return
    }
    GetLaunchOptions()
      .then((options) => setLaunchSSHConnectionID(options?.ssh_connection_id || ''))
      .catch(() => undefined)
  }, [backendReady])

  useEffect(() => {
    if (!terminalTabMenu) {
      return
    }
    const closeMenu = () => setTerminalTabMenu(null)
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closeMenu()
      }
    }
    window.addEventListener('click', closeMenu)
    window.addEventListener('resize', closeMenu)
    window.addEventListener('keydown', closeOnEscape)
    return () => {
      window.removeEventListener('click', closeMenu)
      window.removeEventListener('resize', closeMenu)
      window.removeEventListener('keydown', closeOnEscape)
    }
  }, [terminalTabMenu])

  useEffect(() => {
    if (!terminalConnectionPickerOpen) {
      return
    }
    const closePicker = () => setTerminalConnectionPickerOpen(false)
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closePicker()
      }
    }
    window.addEventListener('click', closePicker)
    window.addEventListener('resize', closePicker)
    window.addEventListener('keydown', closeOnEscape)
    return () => {
      window.removeEventListener('click', closePicker)
      window.removeEventListener('resize', closePicker)
      window.removeEventListener('keydown', closeOnEscape)
    }
  }, [terminalConnectionPickerOpen])

  const nodes = useMemo(() => {
    return proxyNodes.map((node, index) => toDashboardNode(node, index, proxyTestResults[node.name], testingNodeNames.has(node.name)))
  }, [proxyNodes, proxyTestResults, testingNodeNames])

  const sshTunnels = useMemo(() => tunnels.filter((item) => normalizeMode(item.config.mode) !== 'vpn'), [tunnels])
  const tunnelRows = useMemo(() => sshTunnels.map(toTunnelRow), [sshTunnels])
  const sshRows = useMemo(() => sshConnections.map(toSSHRow), [sshConnections])
  const filteredSSHRows = useMemo(() => filterSSHRows(sshRows, terminalConnectionSearch), [sshRows, terminalConnectionSearch])
  const activeTerminalTab = useMemo(() => {
    if (!activeTerminalTabID) {
      return terminalTabs[0]
    }
    return terminalTabs.find((tab) => tab.id === activeTerminalTabID) ?? terminalTabs[0]
  }, [activeTerminalTabID, terminalTabs])

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

  useEffect(() => {
    if (!launchSSHConnectionID || handledLaunchSSHConnectionID === launchSSHConnectionID || sshRows.length === 0) {
      return
    }
    const row = sshRows.find((item) => item.source?.id === launchSSHConnectionID)
    if (!row) {
      return
    }
    setHandledLaunchSSHConnectionID(launchSSHConnectionID)
    setSelectedSSHID(row.id)
    void openTerminal(row)
  }, [handledLaunchSSHConnectionID, launchSSHConnectionID, sshRows])

  useEffect(() => {
    if (terminalTabs.length === 0) {
      if (activeTerminalTabID) {
        setActiveTerminalTabID('')
      }
      return
    }
    if (!activeTerminalTabID || !terminalTabs.some((tab) => tab.id === activeTerminalTabID)) {
      setActiveTerminalTabID(terminalTabs[0].id)
    }
  }, [activeTerminalTabID, terminalTabs])

  const selectedNode = nodes.find((node) => node.id === selectedNodeID) ?? nodes[0]
  const selectedTunnel = tunnelRows.find((row) => row.id === selectedTunnelID) ?? tunnelRows[0]
  const selectedSSH = sshRows.find((row) => row.id === selectedSSHID) ?? sshRows[0]
  const proxyState = proxyStatus?.state ?? 'stopped'
  const connected = proxyState === 'running'
  const connectionStateText = proxyState === 'error' ? '连接异常' : connected ? '已连接' : '未连接'
  const connectionDotClass = proxyState === 'error' ? 'red' : connected ? 'green' : 'gray'
  const proxyMode = normalizeProxyMode(clientSettings.proxy_mode || proxyStatus?.mode || 'rule')
  const runningNode = nodes.find((node) => node.name === proxyStatus?.node_name)
  const currentNode = connected || proxyState === 'error' ? runningNode : undefined
  const homePreviewNodes = useMemo(() => topLatencyNodes(nodes), [nodes])
  const tunnelUploadBytes = tunnels.reduce((sum, item) => sum + (item.stats?.bytes_out ?? 0), 0)
  const tunnelDownloadBytes = tunnels.reduce((sum, item) => sum + (item.stats?.bytes_in ?? 0), 0)
  const uploadTotalBytes = proxyStatus?.upload_total_bytes ?? tunnelUploadBytes
  const downloadTotalBytes = proxyStatus?.download_total_bytes ?? tunnelDownloadBytes
  const uploadSpeedBps = proxyStatus?.upload_speed_bps ?? 0
  const downloadSpeedBps = proxyStatus?.download_speed_bps ?? 0
  const connectedSince = proxyStatus?.started_at ? formatClockDuration(proxyStatus.started_at) : '00:00:00'
  const testingNodes = testingNodeNames.size > 0
  const filteredLogs = useMemo(() => filterLogs(logs, logLevelFilter, logRangeFilter, logSearch), [logs, logLevelFilter, logRangeFilter, logSearch])

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
    if (testingNodes) {
      return
    }
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

    const testNames = new Set(testableNodes.map((node) => node.source.name))
    setNotice('')
    setError('')
    setTestingNodeNames(testNames)
    setProxyTestResults((current) => {
      const next = { ...current }
      testNames.forEach((name) => {
        delete next[name]
      })
      return next
    })
    const batchID = createProxyTestBatchID()
    const completedNames = new Set<string>()
    const applyTestResult = (result?: proxycore.TestResult) => {
      if (!result?.node_name || !testNames.has(result.node_name) || completedNames.has(result.node_name)) {
        return
      }
      completedNames.add(result.node_name)
      setProxyTestResults((current) => ({ ...current, [result.node_name]: result }))
      setTestingNodeNames((current) => {
        const next = new Set(current)
        next.delete(result.node_name)
        return next
      })
    }
    const offProgress = EventsOn('proxy:test-result', (event: ProxyTestProgressEvent) => {
      if (event?.batch_id !== batchID) {
        return
      }
      applyTestResult(event.result)
    })
    try {
      const results = await TestProxyNodes(testableNodes.map((node) => node.source.name), batchID)
      results.forEach(applyTestResult)
      setTestingNodeNames(new Set())
      await refresh()
    } catch (err) {
      const message = errorMessage(err)
      testableNodes.forEach((node) => {
        applyTestResult({
          node_name: node.source.name,
          ok: false,
          latency_ms: 0,
          error: message,
          tested_at: new Date().toISOString(),
        } as proxycore.TestResult)
      })
      setError(message)
    } finally {
      offProgress()
      setTestingNodeNames(new Set())
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

  function markRouteRulesDirty(nextRules: config.ProxyRule[]) {
    routeRulesDirtyRef.current = true
    setRouteRulesDirty(true)
    setRouteRules(nextRules)
  }

  function addRouteRule(outbound: ProxyRuleOutbound) {
    markRouteRulesDirty([...routeRules, createRouteRule(outbound)])
  }

  function updateRouteRule(id: string, patch: Partial<config.ProxyRule>) {
    markRouteRulesDirty(routeRules.map((rule) => (rule.id === id ? { ...rule, ...patch } : rule)))
  }

  function deleteRouteRule(id: string) {
    markRouteRulesDirty(routeRules.filter((rule) => rule.id !== id))
  }

  function resetRouteRules() {
    markRouteRulesDirty(cloneRouteRules(defaultRouteRules))
  }

  async function saveRouteRules() {
    if (busy) {
      return
    }
    const invalid = routeRules.find((rule) => rule.enabled && !rule.value.trim())
    if (invalid) {
      setError('请填写已启用规则的匹配内容')
      return
    }
    const hasWhitespace = routeRules.find((rule) => rule.enabled && /\s/.test(rule.value.trim()))
    if (hasWhitespace) {
      setError('匹配内容不能包含空格或换行')
      return
    }
    const rules = sanitizeRouteRules(routeRules)
    await runAction('保存路由规则', async () => {
      await SetProxyRules(rules)
      routeRulesDirtyRef.current = false
      setRouteRulesDirty(false)
      setRouteRules(rules)
    })
  }

  async function toggleTunnel(row: TunnelRow) {
    if (!row.source || busy || tunnelPending[row.id] || (!row.running && tunnelIsTransitioning(row))) {
      return
    }
    if (!backendReady) {
      setNotice('当前为前端预览模式')
      window.setTimeout(() => setNotice(''), 1800)
      return
    }

    const action: TunnelPendingAction = row.running ? 'stopping' : 'starting'
    const label = row.running ? '停止隧道' : '启动隧道'
    const targetState = row.running ? 'stopped' : 'running'
    const timeoutMs = row.running ? TUNNEL_STOP_TIMEOUT_MS : TUNNEL_START_TIMEOUT_MS
    const tunnelName = row.source.config.name

    setNotice('')
    setError('')
    setTunnelPending((current) => ({ ...current, [row.id]: action }))
    try {
      if (row.running) {
        await StopTunnel(tunnelName)
      } else {
        await StartTunnel(tunnelName)
      }

      const status = await waitForTunnelState(tunnelName, targetState, timeoutMs)
      if (status?.state === targetState) {
        setNotice(`${label}成功`)
      } else if (row.running) {
        setNotice('停止已提交，状态稍后自动刷新')
      } else {
        setNotice(status?.state === 'reconnecting' ? '启动已提交，正在重试连接' : '启动已提交，仍在连接中')
      }
      window.setTimeout(() => setNotice(''), 2200)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setTunnelPending((current) => {
        const next = { ...current }
        delete next[row.id]
        return next
      })
    }
  }

  async function waitForTunnelState(name: string, targetState: string, timeoutMs: number) {
    const deadline = Date.now() + timeoutMs
    let latest: manager.Status | undefined

    while (Date.now() <= deadline) {
      const nextTunnels = (await ListTunnels()) ?? []
      setTunnels(nextTunnels)
      latest = nextTunnels.find((item) => item.config.name === name)
      if (!latest || latest.state === targetState) {
        return latest
      }
      await delay(TUNNEL_POLL_INTERVAL_MS)
    }

    const nextTunnels = (await ListTunnels()) ?? []
    setTunnels(nextTunnels)
    return nextTunnels.find((item) => item.config.name === name)
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
      const sessionID = await createSSHSession(conn)
      const tab = terminalTabFromConnection(conn, sessionID)
      setTerminalTabs((current) => [...current, tab])
      setActiveTerminalTabID(tab.id)
      setActiveNav('terminal')
    })
  }

  async function reconnectTerminal(tabID = activeTerminalTab?.id ?? '') {
    const tab = terminalTabs.find((item) => item.id === tabID)
    if (!tab) {
      return
    }
    setTerminalTabMenu(null)
    await runAction('重连 SSH 终端', async () => {
      if (tab.sessionID) {
        await CloseSSHSession(tab.sessionID).catch(() => undefined)
      }
      const sessionID = await createSSHSession(tab)
      setTerminalTabs((current) =>
        current.map((item) => (item.id === tab.id ? { ...item, sessionID, connected: true } : item)),
      )
      setActiveTerminalTabID(tab.id)
      setActiveNav('terminal')
    })
  }

  async function openTerminalTab(tabID = activeTerminalTab?.id ?? '') {
    const tab = terminalTabs.find((item) => item.id === tabID)
    if (!tab) {
      return
    }
    setTerminalTabMenu(null)
    await runAction('新建 SSH 标签', async () => {
      const sessionID = await createSSHSession(tab)
      const nextTab: TerminalTab = {
        ...tab,
        id: newTerminalTabID(tab.connectionID || tab.sessionID),
        sessionID,
        connected: true,
      }
      setTerminalTabs((current) => [...current, nextTab])
      setActiveTerminalTabID(nextTab.id)
      setActiveNav('terminal')
    })
  }

  async function closeTerminal(tabID = activeTerminalTab?.id ?? '') {
    const tab = terminalTabs.find((item) => item.id === tabID)
    if (!tab) {
      return
    }
    setTerminalTabMenu(null)
    if (tab.sessionID) {
      await CloseSSHSession(tab.sessionID).catch(() => undefined)
    }
    const tabIndex = terminalTabs.findIndex((item) => item.id === tab.id)
    const nextTabs = terminalTabs.filter((item) => item.id !== tab.id)
    setTerminalTabs(nextTabs)
    if (activeTerminalTabID === tab.id) {
      setActiveTerminalTabID(nextTabs[Math.min(tabIndex, nextTabs.length - 1)]?.id ?? '')
    }
  }

  function openTerminalTabMenu(event: ReactMouseEvent, tabID: string) {
    event.preventDefault()
    event.stopPropagation()
    setActiveTerminalTabID(tabID)
    setTerminalTabMenu({ tabID, x: event.clientX, y: event.clientY })
  }

  function editTerminalTabConnection(tabID = activeTerminalTab?.id ?? '') {
    const tab = terminalTabs.find((item) => item.id === tabID)
    if (!tab) {
      return
    }
    const row =
      sshRows.find((item) => item.source?.id && item.source.id === tab.connectionID) ??
      sshRows.find((item) => item.source?.addr === tab.addr && item.source?.user === tab.user)
    setTerminalTabMenu(null)
    if (row) {
      setSelectedSSHID(row.id)
      openSSHForm(row)
      return
    }
    setSSHForm({
      id: tab.connectionID,
      name: tab.title,
      addr: tab.addr,
      user: tab.user,
      password: tab.password,
    })
    setModal('ssh')
  }

  function selectSSHConnection(row: SSHRow) {
    setSelectedSSHID(row.id)
  }

  async function clearLogEntries() {
    await runAction('清空日志', async () => {
      await ClearLogs()
      setLogs([])
    })
  }

  function exportLogEntries() {
    if (filteredLogs.length === 0) {
      setNotice('暂无可导出的日志')
      window.setTimeout(() => setNotice(''), 1800)
      return
    }

    const blob = new Blob(['\ufeff', toLogCsv(filteredLogs)], { type: 'text/csv;charset=utf-8' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `safelink-logs-${formatFileTimestamp(new Date())}.csv`
    document.body.appendChild(link)
    link.click()
    link.remove()
    window.setTimeout(() => window.URL.revokeObjectURL(url), 0)
    setNotice(`已导出 ${filteredLogs.length} 条日志`)
    window.setTimeout(() => setNotice(''), 1800)
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <BrandLogo size="large" />
          <span>SafeLink</span>
        </div>

        <nav className="nav-list">
          {navItems.map((item) => (
            <button
              className={`nav-item ${activeNav === item.key ? 'active' : ''} ${item.key === 'settings' ? 'with-divider' : ''}`}
              key={item.key}
              onClick={() => setActiveNav(item.key)}
              type="button"
            >
              <Icon name={item.icon} />
              <span className="nav-label">{item.label}</span>
              {item.badge && <span className="nav-badge">（{item.badge}）</span>}
            </button>
          ))}
        </nav>

        <div className="sidebar-status">
          <div className="status-line">
            <span className={`dot ${connectionDotClass}`} />
            <strong>{connectionStateText}</strong>
          </div>
          <p>当前模式： 规则模式</p>
        </div>
        <span className="version">v{version}</span>
      </aside>

      <main className="workspace">
        {(notice || error) && <div className={`toast ${error ? 'error' : ''}`}>{error || notice}</div>}
        {activeNav === 'home' && renderHomePage()}
        {activeNav === 'nodes' && renderNodesPage()}
        {activeNav === 'subscriptions' && renderSubscriptionsPage()}
        {activeNav === 'tunnels' && renderTunnelsPage()}
        {SSH_TERMINAL_ENABLED && activeNav === 'terminal' && renderTerminalPage()}
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
                <div className="connection-state">{connectionStateText}</div>
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
          {SSH_TERMINAL_ENABLED && renderTerminalCompactCard()}
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
          const cardClass = ['home-node-card', selected ? 'selected' : '', running ? 'running' : ''].filter(Boolean).join(' ')
          return (
            <button
              className={cardClass}
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
                    <option value="remote">内网穿透</option>
                    <option value="dynamic">动态转发</option>
                  </select>
                </label>
              </div>
              <div className="form-grid">
                <FormField
                  helpText={tunnelModeGuides[tunnelForm.mode].listenHelp}
                  label="监听地址"
                  placeholder={tunnelModeGuides[tunnelForm.mode].listenPlaceholder}
                  value={tunnelForm.listen}
                  onChange={(value) => setTunnelForm((current) => ({ ...current, listen: value }))}
                  required
                />
                <FormField
                  disabled={tunnelForm.mode === 'dynamic'}
                  helpText={tunnelModeGuides[tunnelForm.mode].forwardHelp}
                  label="目标地址"
                  placeholder={tunnelModeGuides[tunnelForm.mode].forwardPlaceholder}
                  value={tunnelForm.forward}
                  onChange={(value) => setTunnelForm((current) => ({ ...current, forward: value }))}
                />
              </div>
              <div className="form-grid">
                <FormField label="SSH 地址" placeholder={sshAddrPlaceholder} value={tunnelForm.sshAddr} onChange={(value) => setTunnelForm((current) => ({ ...current, sshAddr: value }))} required />
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
                <FormField label="SSH 地址" placeholder={sshAddrPlaceholder} value={sshForm.addr} onChange={(value) => setSSHForm((current) => ({ ...current, addr: value }))} required />
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
                <button className="primary-btn" disabled={testingNodes || nodes.length === 0} onClick={testAllNodes} type="button">{testingNodes ? '测试中' : '测试节点'}</button>
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
          const cardClass = ['node-card-item', selected ? 'selected' : '', running ? 'running' : ''].filter(Boolean).join(' ')
          return (
            <button
              aria-pressed={selected}
              className={cardClass}
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
    const selectedPendingAction = selectedTunnel ? tunnelPending[selectedTunnel.id] : undefined
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
          {tunnelRows.map((row) => {
            const pendingAction = tunnelPending[row.id]
            const actionBusy = tunnelActionBusy(row, pendingAction, busy)
            return (
              <button className={`tunnel-table-row ${row.id === selectedTunnel?.id ? 'selected' : ''}`} key={row.id} onClick={() => setSelectedTunnelID(row.id)} type="button">
                <span><span className={`dot ${tunnelDotClass(row, pendingAction)}`} />{row.name}</span>
                <span>{row.type}</span>
                <span>{row.listen}</span>
                <span>{row.forward}</span>
                <span className={tunnelStatusClass(row, pendingAction)}>{tunnelShouldSpin(row, pendingAction) && <Spinner small />}{tunnelStatusText(row, pendingAction)}</span>
                <span className="row-actions">
                  <span
                    aria-disabled={actionBusy}
                    className={`row-action-icon ${row.running ? 'running' : ''} ${actionBusy ? 'disabled' : ''}`}
                    onClick={(event) => {
                      event.stopPropagation()
                      void toggleTunnel(row)
                    }}
                    role="button"
                    title={tunnelControlText(row, pendingAction)}
                  >
                    {tunnelShouldSpin(row, pendingAction) ? <Spinner small /> : row.running ? '■' : '▶'}
                  </span>
                  <span onClick={(event) => { event.stopPropagation(); openTunnelForm(row) }}>···</span>
                </span>
              </button>
            )
          })}
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
              <InfoPair label="状态" value={tunnelStatusText(selectedTunnel, selectedPendingAction)} highlight={selectedTunnel.running || Boolean(selectedPendingAction)} />
              {selectedTunnel.lastError && <InfoPair label="最近错误" value={selectedTunnel.lastError} danger />}
              <InfoPair label="更新时间" value={selectedTunnel.updatedAt} />
              <div className="detail-action-row">
                <button className="outline-btn" onClick={() => openTunnelForm(selectedTunnel)} type="button">编辑</button>
                <button className="danger-outline tunnel-toggle-button" disabled={tunnelActionBusy(selectedTunnel, selectedPendingAction, busy)} onClick={() => toggleTunnel(selectedTunnel)} type="button">
                  {tunnelShouldSpin(selectedTunnel, selectedPendingAction) && <Spinner small />}
                  {tunnelControlText(selectedTunnel, selectedPendingAction)}
                </button>
                <button className="danger-soft" disabled={Boolean(busy) || Boolean(selectedPendingAction)} onClick={deleteSelectedTunnel} type="button">删除</button>
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
        <div className="card terminal-workbench">
          <div className="terminal-toolbar">
            <div className="terminal-toolbar-left">
              <button
                className="terminal-connection-trigger"
                onClick={(event) => {
                  event.stopPropagation()
                  setTerminalConnectionPickerOpen((open) => !open)
                }}
                type="button"
              >
                <span className="terminal-icon">&gt;_</span>
                <span>连接管理</span>
              </button>
              <div className="terminal-tabs" role="tablist" aria-label="SSH 终端标签">
                {terminalTabs.map((tab) => {
                    const label = formatSSHConnectionLabel(tab.title, tab.host)
                    const active = tab.id === activeTerminalTab?.id
                    return (
                      <div
                        aria-selected={active}
                        className={`terminal-tab ${active ? 'active' : ''}`}
                        key={tab.id}
                        onClick={() => setActiveTerminalTabID(tab.id)}
                        onContextMenu={(event) => openTerminalTabMenu(event, tab.id)}
                        role="tab"
                      >
                        <span className={`dot ${tab.connected ? 'green' : 'red'}`} />
                        <span title={label}>{label}</span>
                        <button
                          className="terminal-tab-delete"
                          onClick={(event) => {
                            event.stopPropagation()
                            void closeTerminal(tab.id)
                          }}
                          title="关闭标签"
                          aria-label={`关闭标签 ${label}`}
                          type="button"
                        >
                          ×
                        </button>
                      </div>
                    )
                  })}
              </div>
            </div>
          </div>
          {terminalConnectionPickerOpen && (
            <div className="terminal-connection-popover" onClick={(event) => event.stopPropagation()}>
              <div className="terminal-picker-head">
                <div className="terminal-manager-tools" aria-label="连接管理器工具">
                  <button
                    className="terminal-tool-button add"
                    onClick={() => {
                      setTerminalConnectionPickerOpen(false)
                      openSSHForm()
                    }}
                    title="新建连接"
                    type="button"
                  >
                    ＋
                  </button>
                  <button
                    className="terminal-tool-button"
                    disabled={!selectedSSH}
                    onClick={() => {
                      setTerminalConnectionPickerOpen(false)
                      openSSHForm(selectedSSH)
                    }}
                    title="编辑连接"
                    type="button"
                  >
                    ✎
                  </button>
                  <button
                    className="terminal-tool-button"
                    disabled={!selectedSSH}
                    onClick={() => {
                      setTerminalConnectionPickerOpen(false)
                      void openTerminal()
                    }}
                    title="打开终端"
                    type="button"
                  >
                    ▶
                  </button>
                  <button
                    className="terminal-tool-button danger"
                    disabled={!selectedSSH}
                    onClick={() => {
                      setTerminalConnectionPickerOpen(false)
                      void deleteSelectedSSH()
                    }}
                    title="删除连接"
                    type="button"
                  >
                    −
                  </button>
                </div>
                <div className="terminal-manager-filters">
                  <label className="terminal-manager-search">
                    <span>⌕</span>
                    <input
                      aria-label="搜索连接"
                      placeholder="搜索连接"
                      value={terminalConnectionSearch}
                      onChange={(event) => setTerminalConnectionSearch(event.target.value)}
                    />
                  </label>
                  <select aria-label="连接筛选" value="all" onChange={() => undefined}>
                    <option value="all">全部</option>
                  </select>
                </div>
              </div>
              <div className="terminal-manager-path"><span className="terminal-folder-icon" />连接</div>
              <div className="terminal-connection-table">
                <div className="terminal-connection-head" role="row">
                  <span>名称</span>
                  <span>主机</span>
                  <span>端口</span>
                  <span>用户</span>
                </div>
                <div className="terminal-connection-list">
                  {filteredSSHRows.map((row) => {
                    const target = sshRowTarget(row)
                    return (
                      <button
                        className={`terminal-connection ${row.id === selectedSSH?.id ? 'selected' : ''}`}
                        key={row.id}
                        onClick={() => selectSSHConnection(row)}
                        onDoubleClick={() => {
                          selectSSHConnection(row)
                          setTerminalConnectionPickerOpen(false)
                          void openTerminal(row)
                        }}
                        type="button"
                      >
                        <span><span className="terminal-row-icon" />{row.name}</span>
                        <span title={target.host}>{target.host}</span>
                        <span>{target.port}</span>
                        <span title={target.user}>{target.user}</span>
                      </button>
                    )
                  })}
                  {filteredSSHRows.length === 0 && <EmptyList text={sshRows.length === 0 ? '暂无 SSH 终端连接' : '没有匹配的连接'} />}
                </div>
              </div>
            </div>
          )}
          <div className="terminal-screen">
            {terminalTabs.length > 0 ? (
              terminalTabs.map((tab) => (
                <div className={`terminal-pane-slot ${tab.id === activeTerminalTab?.id ? 'active' : ''}`} key={`${tab.id}-${tab.sessionID}`}>
                  <SSHTerminalPane active={tab.id === activeTerminalTab?.id} sessionID={tab.sessionID} />
                </div>
              ))
            ) : null}
          </div>
          {terminalTabMenu && (
            <div
              className="terminal-tab-menu"
              style={{ left: terminalTabMenu.x, top: terminalTabMenu.y }}
              onClick={(event) => event.stopPropagation()}
            >
              <button onClick={() => editTerminalTabConnection(terminalTabMenu.tabID)} type="button">编辑连接</button>
              <button onClick={() => reconnectTerminal(terminalTabMenu.tabID)} type="button">重连</button>
              <button onClick={() => openTerminalTab(terminalTabMenu.tabID)} type="button">新建标签</button>
              <button className="danger" onClick={() => closeTerminal(terminalTabMenu.tabID)} type="button">关闭标签</button>
            </div>
          )}
        </div>
      </section>
    )
  }

  function renderSettingsPage() {
    return (
      <section className="page-card settings-page">
        <div className="settings-tabs">
          {settingsTabs.map((item) => (
            <button className={activeSettingsTab === item.key ? 'active' : ''} key={item.key} onClick={() => setActiveSettingsTab(item.key)} type="button">{item.label}</button>
          ))}
        </div>
        {activeSettingsTab === 'general' ? renderGeneralSettings() : renderRoutingSettings()}
      </section>
    )
  }

  function renderGeneralSettings() {
    return (
      <div className="settings-form">
        <h3>启动设置</h3>
        <div className="settings-toggle-grid">
          <ToggleRow checked={clientSettings.auto_start} disabled={Boolean(busy)} label="开机启动" onToggle={toggleAutoStart} />
          <ToggleRow checked={clientSettings.system_proxy} disabled={Boolean(busy) || (!connected && !selectedNode)} label="系统代理" onToggle={toggleSystemProxy} title={clientSettings.system_proxy ? '关闭系统代理并断开连接' : '开启系统代理并连接当前节点'} />
        </div>

        <h3>代理模式</h3>
        <div className="segment settings-segment">
          {proxyModeOptions.map((item) => (
            <button className={proxyMode === item.value ? 'selected' : ''} disabled={Boolean(busy)} key={item.value} onClick={() => changeProxyMode(item.value)} type="button">{item.label}</button>
          ))}
        </div>

        <h3>本机信息</h3>
        <div className="settings-info-grid">
          <InfoPair label="主机名" value={machineInfo.hostname || '-'} />
          <InfoPair label="当前用户" value={machineInfo.username || '-'} />
          <InfoPair label="操作系统" value={formatMachineOS(machineInfo.os)} />
          <InfoPair label="系统架构" value={machineInfo.arch || '-'} />
          <InfoPair label="CPU 核心" value={machineInfo.cpu_cores || '-'} />
          <InfoPair label="本机 IP" value={formatMachineIPs(machineInfo.ips)} />
        </div>
      </div>
    )
  }

  function renderRoutingSettings() {
    return (
      <div className="settings-form route-settings">
        <div className="settings-section-title">
          <h3>转发规则</h3>
          <div className="route-rule-actions">
            <button className="small-outline" onClick={() => addRouteRule('direct')} type="button">＋ 白名单</button>
            <button className="small-outline" onClick={() => addRouteRule('selected')} type="button">＋ 代理</button>
            <button className="small-outline" onClick={() => addRouteRule('block')} type="button">＋ 阻断</button>
            <button className="small-outline" disabled={Boolean(busy)} onClick={resetRouteRules} type="button">恢复默认</button>
            <button className="primary-btn" disabled={Boolean(busy) || !routeRulesDirty} onClick={saveRouteRules} type="button">保存规则</button>
          </div>
        </div>
        <div className="route-rule-table">
          <div className="route-rule-head">
            <span>启用</span>
            <span>名称</span>
            <span>类型</span>
            <span>匹配内容</span>
            <span>策略</span>
            <span>操作</span>
          </div>
          {routeRules.map((rule) => (
            <div className={`route-rule-row ${rule.enabled && !rule.value.trim() ? 'invalid' : ''}`} key={rule.id}>
              <label className="route-enable">
                <input checked={rule.enabled} onChange={(event) => updateRouteRule(rule.id, { enabled: event.target.checked })} type="checkbox" />
                <span className={rule.enabled ? 'checkbox checked' : 'checkbox'} />
              </label>
              <input value={rule.name} onChange={(event) => updateRouteRule(rule.id, { name: event.target.value })} />
              <select value={normalizeRuleType(rule.type)} onChange={(event) => updateRouteRule(rule.id, { type: event.target.value as ProxyRuleType })}>
                {routeRuleTypeOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
              <input placeholder={routeRulePlaceholder(rule.type)} value={rule.value} onChange={(event) => updateRouteRule(rule.id, { value: event.target.value })} />
              <select value={normalizeRuleOutbound(rule.outbound)} onChange={(event) => updateRouteRule(rule.id, { outbound: event.target.value as ProxyRuleOutbound })}>
                {routeOutboundOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
              <button className="small-outline danger-action" onClick={() => deleteRouteRule(rule.id)} type="button">删除</button>
            </div>
          ))}
          {routeRules.length === 0 && <EmptyTable text="暂无路由规则" />}
        </div>
      </div>
    )
  }

  function renderLogsPage() {
    return (
      <section className="page-card logs-page">
        <div className="logs-toolbar">
          <SelectLike label="日志级别" options={logLevelOptions} value={logLevelFilter} onChange={(value) => setLogLevelFilter(value as LogLevelFilter)} />
          <SelectLike label="时间范围" options={logRangeOptions} value={logRangeFilter} onChange={(value) => setLogRangeFilter(value as LogRangeFilter)} />
          <SearchBox placeholder="搜索日志内容" value={logSearch} onChange={setLogSearch} />
          <button className="small-outline" onClick={exportLogEntries} type="button">导出日志</button>
          <button className="primary-btn" onClick={clearLogEntries} type="button">清空日志</button>
        </div>
        <div className="logs-table">
          <div className="logs-head">
            <span>时间</span>
            <span>级别</span>
            <span>模块</span>
            <span>内容</span>
          </div>
          {filteredLogs.map((item, index) => (
            <div className="logs-row" key={`${item.time}-${index}`}>
              <span>{formatOptionalDate(item.time)}</span>
              <span className={item.level === 'error' ? 'danger-text' : item.level === 'warn' ? 'warn-text' : 'good-text'}>{logLevelLabel(item.level)}</span>
              <span>{item.module}</span>
              <span>{item.message}</span>
            </div>
          ))}
        </div>
        <div className="logs-bottom">
          {filteredLogs.length === 0 && <div className="logs-empty-bottom">{logs.length === 0 ? '暂无日志数据' : '暂无匹配日志'}</div>}
          <PageFooter count={filteredLogs.length} />
        </div>
      </section>
    )
  }

  function renderTunnelCompactCard() {
    return (
      <div className="card compact-card">
        <PanelTitle title="SSH 隧道" action={<button className="small-outline" onClick={() => openTunnelForm()} type="button">＋ 新建隧道</button>} />
        <div className="compact-list">
          {tunnelRows.map((row) => {
            const pendingAction = tunnelPending[row.id]
            return (
              <div className="compact-row" key={row.id}>
                <button className={`round-play ${row.running ? 'running' : ''}`} disabled={tunnelActionBusy(row, pendingAction, busy)} onClick={() => toggleTunnel(row)} title={tunnelControlText(row, pendingAction)} type="button">
                  {tunnelShouldSpin(row, pendingAction) ? <Spinner small /> : row.running ? '■' : '▶'}
                </button>
                <div><strong>{row.name}</strong><p>{row.detail}</p></div>
                <span className={tunnelStatusClass(row, pendingAction)}>{tunnelShouldSpin(row, pendingAction) ? <Spinner small /> : <span className={`dot ${tunnelDotClass(row, pendingAction)}`} />}{tunnelStatusText(row, pendingAction)}</span>
                <button className="ellipsis" type="button">···</button>
              </div>
            )
          })}
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
  placeholder = '',
  helpText = '',
}: {
  label: string
  value: string
  onChange: (value: string) => void
  type?: string
  required?: boolean
  disabled?: boolean
  placeholder?: string
  helpText?: string
}) {
  const inputID = useId()
  const [passwordVisible, setPasswordVisible] = useState(false)
  const isPassword = type === 'password'
  const inputType = isPassword && passwordVisible ? 'text' : type

  return (
    <div className="form-field">
      <label className="form-field-label" htmlFor={inputID}>
        <span>{label}</span>
        {helpText && (
          <span
            aria-label={`${label}说明：${helpText}`}
            className="field-help"
            data-tooltip={helpText}
            tabIndex={0}
          >
            !
          </span>
        )}
      </label>
      <div className={isPassword ? 'password-field' : undefined}>
        <input id={inputID} disabled={disabled} placeholder={placeholder} required={required} type={inputType} value={value} onChange={(event) => onChange(event.target.value)} />
        {isPassword && (
          <button
            aria-label={passwordVisible ? '隐藏密码' : '显示密码'}
            aria-pressed={passwordVisible}
            className={`password-toggle ${passwordVisible ? 'visible' : ''}`}
            disabled={disabled}
            onClick={() => setPasswordVisible((current) => !current)}
            title={passwordVisible ? '隐藏密码' : '显示密码'}
            type="button"
          >
            <span className="eye-icon" aria-hidden="true" />
          </button>
        )}
      </div>
    </div>
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

function SearchBox({ placeholder, value, onChange }: { placeholder: string; value: string; onChange: (value: string) => void }) {
  return <label className="search-box"><span>⌕</span><input placeholder={placeholder} value={value} onChange={(event) => onChange(event.target.value)} /></label>
}

function SelectLike({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: Array<{ value: string; label: string }>
  onChange: (value: string) => void
}) {
  return (
    <label className="select-like">
      <span>{label}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
      </select>
    </label>
  )
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

function Spinner({ small = false }: { small?: boolean }) {
  return <span className={`spinner ${small ? 'small' : ''}`} aria-hidden="true" />
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
  const type = mode === 'remote' ? '内网穿透' : mode === 'dynamic' ? '动态转发' : '本地转发'
  return {
    id: item.config.name,
    name: item.config.name,
    type,
    listen,
    forward,
    detail: mode === 'dynamic' ? `${listen} (${forward})` : `${listen} → ${forward}`,
    endpoint: `${item.config.ssh?.user || '-'}@${item.config.ssh?.addr || '-'}`,
    state: item.state || 'stopped',
    lastError: item.last_error || '',
    running: item.state === 'running',
    updatedAt: item.started_at ? formatOptionalDate(item.started_at) : '-',
    source: item,
  }
}

function tunnelIsTransitioning(row: TunnelRow) {
  return row.state === 'connecting' || row.state === 'reconnecting'
}

function tunnelShouldSpin(row: TunnelRow, pendingAction?: TunnelPendingAction) {
  return Boolean(pendingAction) || tunnelIsTransitioning(row)
}

function tunnelActionBusy(row: TunnelRow, pendingAction: TunnelPendingAction | undefined, busy: string) {
  return Boolean(busy) || Boolean(pendingAction) || (!row.running && tunnelIsTransitioning(row))
}

function tunnelStatusText(row: TunnelRow, pendingAction?: TunnelPendingAction) {
  if (pendingAction === 'starting') {
    return '启动中'
  }
  if (pendingAction === 'stopping') {
    return '停止中'
  }
  if (row.state === 'connecting') {
    return '连接中'
  }
  if (row.state === 'reconnecting') {
    return '重连中'
  }
  return row.running ? '运行中' : '已停止'
}

function tunnelControlText(row: TunnelRow, pendingAction?: TunnelPendingAction) {
  if (pendingAction === 'starting' || (!row.running && tunnelIsTransitioning(row))) {
    return '启动中'
  }
  if (pendingAction === 'stopping') {
    return '停止中'
  }
  return row.running ? '停止' : '启动'
}

function tunnelStatusClass(row: TunnelRow, pendingAction?: TunnelPendingAction) {
  const classes = ['mini-state']
  if (row.running) {
    classes.push('running')
  }
  if (pendingAction || tunnelIsTransitioning(row)) {
    classes.push('pending')
  }
  return classes.join(' ')
}

function tunnelDotClass(row: TunnelRow, pendingAction?: TunnelPendingAction) {
  if (pendingAction || tunnelIsTransitioning(row)) {
    return 'yellow'
  }
  return row.running ? 'green' : 'gray'
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

function filterSSHRows(rows: SSHRow[], keyword: string) {
  const query = keyword.trim().toLowerCase()
  if (!query) {
    return rows
  }
  return rows.filter((row) => {
    const target = sshRowTarget(row)
    return [row.name, row.detail, target.host, target.port, target.user].some((value) => value.toLowerCase().includes(query))
  })
}

function sshRowTarget(row: SSHRow) {
  return {
    host: hostFromAddr(row.source?.addr || '-') || '-',
    port: portFromAddr(row.source?.addr || '') || '22',
    user: row.source?.user || '-',
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

function mergeClientSettings(settings?: store.ClientSettings | null): store.ClientSettings {
  const merged = { ...defaultClientSettings, ...(settings ?? {}) } as store.ClientSettings
  merged.rule_mode_rules = normalizeRouteRuleDrafts(settings?.rule_mode_rules ?? defaultRouteRules)
  return merged
}

function cloneRouteRules(rules: config.ProxyRule[]) {
  return rules.map((rule) => ({ ...rule }))
}

function normalizeRouteRuleDrafts(rules?: config.ProxyRule[] | null): config.ProxyRule[] {
  const source = rules ?? defaultRouteRules
  return source.map((rule) => ({
    id: rule.id || newRouteRuleID(),
    name: rule.name || routeRuleName(normalizeRuleOutbound(rule.outbound)),
    type: normalizeRuleType(rule.type),
    value: rule.value || '',
    outbound: normalizeRuleOutbound(rule.outbound),
    enabled: rule.enabled !== false,
  }))
}

function sanitizeRouteRules(rules: config.ProxyRule[]) {
  return normalizeRouteRuleDrafts(rules).map((rule) => ({
    ...rule,
    name: rule.name.trim() || routeRuleName(normalizeRuleOutbound(rule.outbound)),
    value: rule.value.trim(),
  }))
}

function createRouteRule(outbound: ProxyRuleOutbound): config.ProxyRule {
  const type: ProxyRuleType = outbound === 'selected' ? 'domain_suffix' : 'ip_cidr'
  return {
    id: newRouteRuleID(),
    name: routeRuleName(outbound),
    type,
    value: '',
    outbound,
    enabled: true,
  }
}

function normalizeRuleType(value?: string): ProxyRuleType {
  if (value === 'domain' || value === 'domain_suffix' || value === 'domain_keyword' || value === 'ip_cidr' || value === 'rule_set') {
    return value
  }
  return 'domain_suffix'
}

function normalizeRuleOutbound(value?: string): ProxyRuleOutbound {
  if (value === 'selected' || value === 'direct' || value === 'block') {
    return value
  }
  return 'direct'
}

function routeRulePlaceholder(type?: string) {
  switch (normalizeRuleType(type)) {
    case 'domain':
      return 'example.com'
    case 'domain_keyword':
      return 'example'
    case 'ip_cidr':
      return '192.168.0.0/16'
    case 'rule_set':
      return 'geosite-geolocation-cn'
    default:
      return 'example.com'
  }
}

function routeRuleName(outbound: ProxyRuleOutbound) {
  if (outbound === 'selected') {
    return '代理规则'
  }
  if (outbound === 'block') {
    return '阻断规则'
  }
  return '白名单'
}

function newRouteRuleID() {
  return `rule-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
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

function portFromAddr(addr: string) {
  const value = (addr || '').trim()
  if (!value) {
    return ''
  }
  if (value.startsWith('[')) {
    const end = value.indexOf(']')
    if (end > 0 && value[end + 1] === ':') {
      return value.slice(end + 2) || ''
    }
    return ''
  }
  const parts = value.split(':')
  return parts.length === 2 ? parts[1] || '' : ''
}

type SSHSessionTarget = Pick<store.SSHConnection, 'addr' | 'user' | 'password'>

function createSSHSession(target: SSHSessionTarget) {
  return CreateSSHSession({
    addr: target.addr,
    user: target.user,
    identity_file: '',
    passphrase: '',
    password: target.password,
    rows: 24,
    cols: 80,
  })
}

function terminalTabFromConnection(conn: store.SSHConnection, sessionID: string): TerminalTab {
  const host = hostFromAddr(conn.addr)
  return {
    id: newTerminalTabID(conn.id || sessionID),
    sessionID,
    connectionID: conn.id,
    title: conn.name || `${conn.user}@${host}`,
    host,
    addr: conn.addr,
    user: conn.user,
    password: conn.password,
    connected: true,
  }
}

function newTerminalTabID(seed: string) {
  return `${seed || 'ssh'}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

const terminalInputEncoder = new TextEncoder()

function base64FromBytes(bytes: Uint8Array) {
  let binary = ''
  const chunkSize = 0x8000
  for (let index = 0; index < bytes.length; index += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(index, index + chunkSize))
  }
  return btoa(binary)
}

function bytesFromBase64(data: string) {
  const binary = atob(data)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index) & 0xff
  }
  return bytes
}

function encodeTerminalText(data: string) {
  return base64FromBytes(terminalInputEncoder.encode(data))
}

function encodeTerminalBinary(data: string) {
  const bytes = new Uint8Array(data.length)
  for (let index = 0; index < data.length; index += 1) {
    bytes[index] = data.charCodeAt(index) & 0xff
  }
  return base64FromBytes(bytes)
}

function describeTerminalText(data: string) {
  const codes = Array.from(data.slice(0, 32), (char) => char.charCodeAt(0).toString(16).padStart(2, '0')).join('')
  return `len=${data.length} hex=${codes} text=${JSON.stringify(data.slice(0, 32))}`
}

function describeTerminalGeometry(container: HTMLElement, term: Terminal) {
  const xterm = container.querySelector<HTMLElement>('.xterm')
  const viewport = container.querySelector<HTMLElement>('.xterm-viewport')
  const screen = container.querySelector<HTMLElement>('.xterm-screen')
  const canvas = container.querySelector<HTMLCanvasElement>('.xterm-screen canvas')
  const part = (name: string, element: HTMLElement | null) => {
    if (!element) {
      return `${name}=missing`
    }
    const rect = element.getBoundingClientRect()
    return `${name}=${element.clientWidth}x${element.clientHeight}/${Math.round(rect.width)}x${Math.round(rect.height)}`
  }
  return [
    `rows=${term.rows}`,
    `cols=${term.cols}`,
    part('container', container),
    part('xterm', xterm),
    part('viewport', viewport),
    part('screen', screen),
    canvas ? `canvas=${canvas.width}x${canvas.height}/${canvas.clientWidth}x${canvas.clientHeight}` : 'canvas=missing',
    viewport ? `scroll=${viewport.scrollTop}/${viewport.scrollHeight}` : 'scroll=missing',
  ].join(' ')
}

function describeTerminalKey(event: KeyboardEvent) {
  return [
    `type=${event.type}`,
    `key=${JSON.stringify(event.key)}`,
    `code=${event.code}`,
    `ctrl=${event.ctrlKey ? 1 : 0}`,
    `shift=${event.shiftKey ? 1 : 0}`,
    `alt=${event.altKey ? 1 : 0}`,
    `meta=${event.metaKey ? 1 : 0}`,
    `repeat=${event.repeat ? 1 : 0}`,
    `target=${event.target instanceof HTMLElement ? event.target.className || event.target.tagName : ''}`,
  ].join(' ')
}

function SSHTerminalPane({ active = true, sessionID }: { active?: boolean; sessionID: string }) {
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
      cursorStyle: 'block',
      fontFamily: 'Consolas, "Cascadia Mono", "Courier New", monospace',
      fontSize: 13,
      ignoreBracketedPasteMode: false,
      macOptionIsMeta: true,
      rightClickSelectsWord: true,
      scrollOnUserInput: true,
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
    fit.fit()
    terminalRef.current = term
    fitAddonRef.current = fit
    ResizeSSHSession(sessionID, term.rows, term.cols).catch(() => undefined)

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
        term.paste(text)
      }
    }
    const pasteClipboard = () => {
      readClipboard().then(pasteText).catch(() => undefined)
    }

    const recordDebug = (source: string, detail: string) => {
      RecordSSHDebug(sessionID, source, detail).catch(() => undefined)
    }
    recordDebug('frontend-open', describeTerminalGeometry(container, term))

    let disposed = false
    let inputQueue: string[] = []
    let flushingInput = false
    const flushInputQueue = () => {
      if (flushingInput || disposed) {
        return
      }
      flushingInput = true
      void (async () => {
        while (!disposed && inputQueue.length > 0) {
          const batch = inputQueue.splice(0)
          try {
            await SendSSHInputBase64Batch(sessionID, batch)
          } catch (err) {
            inputQueue = []
            if (!disposed) {
              term.writeln(`\r\n[错误] ${errorMessage(err)}`)
            }
            break
          }
        }
      })().finally(() => {
        flushingInput = false
        if (!disposed && inputQueue.length > 0) {
          flushInputQueue()
        }
      })
    }
    const queueTerminalInput = (payload: string) => {
      if (!payload || disposed) {
        return
      }
      inputQueue.push(payload)
      flushInputQueue()
    }
    const dataDisposable = term.onData((data) => {
      recordDebug('frontend-data', describeTerminalText(data))
      queueTerminalInput(encodeTerminalText(data))
    })
    const binaryDisposable = term.onBinary((data) => {
      recordDebug('frontend-binary', describeTerminalText(data))
      queueTerminalInput(encodeTerminalBinary(data))
    })
    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown') {
        return true
      }
      recordDebug('frontend-keydown', describeTerminalKey(event))
      const key = event.key.toLowerCase()
      const copyShortcut = (event.ctrlKey || event.metaKey) && key === 'c'
      const pasteShortcut = ((event.ctrlKey || event.metaKey) && event.shiftKey && key === 'v')
      const pasteInsertShortcut = event.shiftKey && key === 'insert'
      if (copyShortcut && term.hasSelection()) {
        event.preventDefault()
        copySelection()
        return false
      }
      if (pasteShortcut || pasteInsertShortcut) {
        event.preventDefault()
        pasteClipboard()
        return false
      }
      return true
    })
    const writeTerminalOutput = (output: string | Uint8Array) => {
      if (disposed) {
        return
      }
      if (typeof output === 'string' && output.length === 0) {
        return
      }
      if (output instanceof Uint8Array && output.length === 0) {
        return
      }
      term.write(output)
    }
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
    const textarea = container.querySelector('.xterm-helper-textarea')
    const handleFocus = () => recordDebug('frontend-focus', 'term-focus')
    const handleBlur = () => recordDebug('frontend-blur', 'term-blur')
    textarea?.addEventListener('focus', handleFocus)
    textarea?.addEventListener('blur', handleBlur)
    const offOutput = EventsOn('ssh:output', (event: SSHOutputEvent) => {
      if (event?.session_id === sessionID) {
        if (event.data_base64) {
          const output = bytesFromBase64(event.data_base64)
          recordDebug('frontend-output', `bytes=${output.length}`)
          writeTerminalOutput(output)
          return
        }
        recordDebug('frontend-output', `text=${event.data?.length ?? 0}`)
        writeTerminalOutput(event.data)
      }
    })
    const offClosed = EventsOn('ssh:closed', (event: SSHClosedEvent) => {
      if (event?.session_id === sessionID) {
        term.writeln(`\r\n[连接已关闭${event.message ? `：${event.message}` : ''}]`)
      }
    })
    const resizeDisposable = term.onResize(({ rows, cols }) => {
      ResizeSSHSession(sessionID, rows, cols).catch(() => undefined)
    })
    let resizeFrame = 0
    const resize = () => {
      if (!container.isConnected || container.clientWidth <= 0 || container.clientHeight <= 0) {
        return
      }
      fit.fit()
      recordDebug('frontend-resize', describeTerminalGeometry(container, term))
    }
    const scheduleResize = () => {
      window.cancelAnimationFrame(resizeFrame)
      resizeFrame = window.requestAnimationFrame(resize)
    }
    const resizeObserver = new ResizeObserver(scheduleResize)
    resizeObserver.observe(container)
    window.addEventListener('resize', scheduleResize)
    window.setTimeout(scheduleResize, 0)

    return () => {
      disposed = true
      inputQueue = []
      dataDisposable.dispose()
      binaryDisposable.dispose()
      resizeDisposable.dispose()
      textarea?.removeEventListener('focus', handleFocus)
      textarea?.removeEventListener('blur', handleBlur)
      container.removeEventListener('copy', handleCopy)
      container.removeEventListener('paste', handlePaste)
      container.removeEventListener('contextmenu', handleContextMenu)
      offOutput()
      offClosed()
      resizeObserver.disconnect()
      window.cancelAnimationFrame(resizeFrame)
      window.removeEventListener('resize', scheduleResize)
      term.dispose()
      terminalRef.current = null
      fitAddonRef.current = null
    }
  }, [sessionID])

  useEffect(() => {
    if (!active || !terminalRef.current || !fitAddonRef.current) {
      return
    }
    window.setTimeout(() => {
      fitAddonRef.current?.fit()
      const term = terminalRef.current
      if (!term) {
        return
      }
      ResizeSSHSession(sessionID, term.rows, term.cols).catch(() => undefined)
      term.focus()
    }, 0)
  }, [active, sessionID])

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
    return '未测得'
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
    return '测速失败'
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

function browserMachineInfo(): MachineInfo {
  if (typeof navigator === 'undefined') {
    return {}
  }
  return {
    hostname: '浏览器预览',
    os: inferBrowserOS(navigator.userAgent),
    arch: navigator.platform || '-',
    cpu_cores: navigator.hardwareConcurrency || 0,
    ips: [],
  }
}

function inferBrowserOS(userAgent: string) {
  const text = userAgent.toLowerCase()
  if (text.includes('windows')) {
    return 'windows'
  }
  if (text.includes('mac os') || text.includes('macintosh')) {
    return 'darwin'
  }
  if (text.includes('linux')) {
    return 'linux'
  }
  return ''
}

function formatMachineOS(value?: string) {
  const normalized = (value || '').toLowerCase()
  if (normalized === 'windows') {
    return 'Windows'
  }
  if (normalized === 'darwin') {
    return 'macOS'
  }
  if (normalized === 'linux') {
    return 'Linux'
  }
  return value || '-'
}

function formatMachineIPs(values?: string[]) {
  if (!values || values.length === 0) {
    return '-'
  }
  return values.join('、')
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

function filterLogs(entries: main.LogEntry[], level: LogLevelFilter, range: LogRangeFilter, search: string) {
  const startedAt = logRangeStart(range)
  const query = search.trim().toLowerCase()

  return entries.filter((item) => {
    if (level !== 'all' && normalizeLogLevel(item.level) !== level) {
      return false
    }
    if (startedAt !== null) {
      const time = new Date(item.time).getTime()
      if (!Number.isFinite(time) || time < startedAt) {
        return false
      }
    }
    if (query) {
      const haystack = [formatOptionalDate(item.time), logLevelLabel(item.level), item.module, item.message].join(' ').toLowerCase()
      if (!haystack.includes(query)) {
        return false
      }
    }
    return true
  })
}

function logRangeStart(range: LogRangeFilter) {
  const now = Date.now()
  if (range === '1h') {
    return now - 60 * 60 * 1000
  }
  if (range === '24h') {
    return now - 24 * 60 * 60 * 1000
  }
  if (range === '7d') {
    return now - 7 * 24 * 60 * 60 * 1000
  }
  return null
}

function toLogCsv(entries: main.LogEntry[]) {
  const rows = [
    ['时间', '级别', '模块', '内容'],
    ...entries.map((item) => [formatOptionalDate(item.time), logLevelLabel(item.level), item.module || '', item.message || '']),
  ]
  return `${rows.map((row) => row.map(csvCell).join(',')).join('\r\n')}\r\n`
}

function csvCell(value: string) {
  return /[",\r\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value
}

function createProxyTestBatchID() {
  return `proxy-test-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

function formatFileTimestamp(date: Date) {
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${date.getFullYear()}${pad(date.getMonth() + 1)}${pad(date.getDate())}-${pad(date.getHours())}${pad(date.getMinutes())}${pad(date.getSeconds())}`
}

function normalizeLogLevel(level: string): Exclude<LogLevelFilter, 'all'> {
  const lower = level.toLowerCase()
  if (lower === 'error') {
    return 'error'
  }
  if (lower === 'warn' || lower === 'warning') {
    return 'warn'
  }
  return 'info'
}

function logLevelLabel(level: string) {
  const normalized = normalizeLogLevel(level)
  if (normalized === 'error') {
    return '错误'
  }
  if (normalized === 'warn') {
    return '警告'
  }
  return '信息'
}

function isBackendReady() {
  return Boolean(window.go?.main?.App)
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

function delay(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

export default App
