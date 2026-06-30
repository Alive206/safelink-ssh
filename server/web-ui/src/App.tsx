import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react'

type RuntimeSnapshot = {
  started_at?: string
  uptime_seconds: number
  listen_addr: string
  subnet: string
  nat_iface: string
  nat_enabled: boolean
  padding: boolean
  active_clients: number
  total_clients: number
  total_bytes_in: number
  total_bytes_out: number
  clients: ClientSnapshot[]
}

type ClientSnapshot = {
  id: string
  remote_addr: string
  connected_at: string
  uptime_seconds: number
  bytes_in: number
  bytes_out: number
  last_traffic_at: string
  tun_name?: string
  last_error?: string
  authenticated_as?: string
}

type SubscriptionInfo = {
  enabled: boolean
  name: string
  public_addr: string
  token_enabled: boolean
  json_url: string
  yaml_url: string
  subnet: string
  auto_route: boolean
}

type StatusResponse = {
  status: string
  service: string
  runtime: RuntimeSnapshot
  subscription: SubscriptionInfo
}

type ServerNode = {
  name: string
  mode: string
  forward: string
  ssh: {
    user: string
  }
  tun: {
    subnet: string
    dns?: string[]
    auto_route: boolean
  }
}

const emptyRuntime: RuntimeSnapshot = {
  uptime_seconds: 0,
  listen_addr: '',
  subnet: '',
  nat_iface: '',
  nat_enabled: false,
  padding: false,
  active_clients: 0,
  total_clients: 0,
  total_bytes_in: 0,
  total_bytes_out: 0,
  clients: [],
}

function App() {
  const [authenticated, setAuthenticated] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [status, setStatus] = useState<StatusResponse | null>(null)
  const [clients, setClients] = useState<ClientSnapshot[]>([])
  const [nodes, setNodes] = useState<ServerNode[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const runtime = status?.runtime ?? emptyRuntime
  const subscription = status?.subscription

  const refresh = useCallback(async () => {
    try {
      const [nextStatus, nextClients, nextNodes] = await Promise.all([
        api<StatusResponse>('/api/status'),
        api<{ clients: ClientSnapshot[] }>('/api/runtime/clients'),
        api<ServerNode[]>('/api/vpn/servers'),
      ])
      setStatus(nextStatus)
      setClients(nextClients.clients ?? [])
      setNodes(nextNodes ?? [])
      setAuthenticated(true)
      setError('')
    } catch (err) {
      if (err instanceof APIError && err.status === 401) {
        setAuthenticated(false)
        return
      }
      setError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    api('/api/auth/me')
      .then(() => {
        setAuthenticated(true)
        refresh()
      })
      .catch(() => setAuthenticated(false))
  }, [refresh])

  useEffect(() => {
    if (!authenticated) {
      return
    }
    const timer = window.setInterval(refresh, 5000)
    return () => window.clearInterval(timer)
  }, [authenticated, refresh])

  const totals = useMemo(
    () => [
      { label: '在线客户端', value: runtime.active_clients },
      { label: '累计客户端', value: runtime.total_clients },
      { label: '入站流量', value: formatBytes(runtime.total_bytes_in) },
      { label: '出站流量', value: formatBytes(runtime.total_bytes_out) },
    ],
    [runtime],
  )

  async function login(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api('/api/auth/login', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
        headers: { 'Content-Type': 'application/json' },
      })
      setPassword('')
      setAuthenticated(true)
      await refresh()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function logout() {
    await api('/api/auth/logout', { method: 'POST' }).catch(() => undefined)
    setAuthenticated(false)
    setStatus(null)
    setClients([])
    setNodes([])
  }

  async function copy(text: string) {
    try {
      await copyToClipboard(text)
      setNotice('订阅链接已复制到剪贴板')
      setError('')
    } catch (err) {
      setNotice('')
      setError(errorMessage(err))
    }
  }

  if (!authenticated) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950 px-4 text-slate-100">
        <form className="card w-full max-w-sm space-y-4" onSubmit={login}>
          <div>
            <p className="text-sm font-semibold uppercase tracking-[0.3em] text-cyan-300">SafeLink</p>
            <h1 className="mt-2 text-2xl font-bold">服务端控制台</h1>
            <p className="mt-2 text-sm text-slate-400">使用 `server.yaml` 中配置的账号密码登录。</p>
          </div>
          <label>
            <span className="label">账号</span>
            <input className="input" value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" required />
          </label>
          <label>
            <span className="label">密码</span>
            <input className="input" value={password} onChange={(event) => setPassword(event.target.value)} type="password" autoComplete="current-password" required />
          </label>
          {error && <p className="rounded-lg border border-rose-500/30 bg-rose-500/10 p-3 text-sm text-rose-200">{error}</p>}
          <button className="btn-primary w-full" disabled={busy} type="submit">
            {busy ? '登录中...' : '登录'}
          </button>
        </form>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <header className="border-b border-slate-800 bg-slate-900/80">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-4 px-6 py-5">
          <div>
            <p className="text-sm font-semibold uppercase tracking-[0.3em] text-cyan-300">SafeLink</p>
            <h1 className="mt-1 text-2xl font-bold">服务端控制台</h1>
          </div>
          <div className="flex gap-2">
            <button className="btn-secondary" onClick={refresh} type="button">
              刷新
            </button>
            <button className="btn-secondary" onClick={logout} type="button">
              退出
            </button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-7xl space-y-6 px-6 py-6">
        {notice && <div className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 p-4 text-sm text-emerald-200">{notice}</div>}
        {error && <div className="rounded-xl border border-rose-500/30 bg-rose-500/10 p-4 text-sm text-rose-200">{error}</div>}

        <section className="grid gap-4 md:grid-cols-4">
          {totals.map((item) => (
            <div className="card" key={item.label}>
              <p className="text-sm text-slate-400">{item.label}</p>
              <p className="mt-2 text-3xl font-semibold">{item.value}</p>
            </div>
          ))}
        </section>

        <section className="grid gap-6 xl:grid-cols-[1fr_420px]">
          <div className="card">
            <h2 className="text-lg font-semibold">服务状态</h2>
            <div className="mt-4 grid gap-3 md:grid-cols-2">
              <Info label="服务" value={status?.service ?? '-'} />
              <Info label="运行状态" value={status?.status ?? '-'} valueClass="text-emerald-300" />
              <Info label="监听地址" value={runtime.listen_addr || '-'} />
              <Info label="VPN 子网" value={runtime.subnet || '-'} />
              <Info label="NAT" value={runtime.nat_enabled ? `已启用：${runtime.nat_iface || '-'}` : '未启用'} valueClass={runtime.nat_enabled ? 'text-emerald-300' : 'text-amber-300'} />
              <Info label="运行时长" value={formatDuration(runtime.uptime_seconds)} />
              <Info label="流量填充" value={runtime.padding ? '启用' : '禁用'} />
              <Info label="启动时间" value={runtime.started_at ? new Date(runtime.started_at).toLocaleString() : '-'} />
            </div>
          </div>

          <div className="card">
            <h2 className="text-lg font-semibold">订阅</h2>
            {subscription?.enabled ? (
              <div className="mt-4 space-y-3 text-sm">
                <Info label="节点名称" value={subscription.name || '-'} />
                <Info label="公网地址" value={subscription.public_addr || '-'} />
                <Info label="客户端子网" value={subscription.subnet || '-'} />
                <Info label="Token 保护" value={subscription.token_enabled ? '已启用' : '未启用'} valueClass={subscription.token_enabled ? 'text-emerald-300' : 'text-amber-300'} />
                <LinkRow label="SafeLink JSON" value={subscription.json_url} onCopy={copy} />
                <LinkRow label="Clash YAML" value={subscription.yaml_url} onCopy={copy} />
              </div>
            ) : (
              <p className="mt-4 rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-200">订阅未启用，请配置 `VPN_PUBLIC_ADDR` 或 `--public-addr`。</p>
            )}
          </div>
        </section>

        <section className="card">
          <h2 className="text-lg font-semibold">当前服务端节点</h2>
          <div className="mt-4 grid gap-3 md:grid-cols-2">
            {nodes.length === 0 ? (
              <p className="text-sm text-slate-400">暂无可导出的节点，请检查公网地址和订阅配置。</p>
            ) : (
              nodes.map((node) => (
                <div className="rounded-xl border border-slate-800 bg-slate-950/70 p-4" key={node.name}>
                  <p className="font-semibold">{node.name}</p>
                  <p className="mt-2 text-sm text-slate-400">地址：{node.forward}</p>
                  <p className="mt-1 text-sm text-slate-400">用户：{node.ssh?.user || '-'}</p>
                  <p className="mt-1 text-sm text-slate-400">客户端子网：{node.tun?.subnet || '-'}</p>
                </div>
              ))
            )}
          </div>
        </section>

        <section className="card">
          <div className="flex items-center justify-between gap-4">
            <h2 className="text-lg font-semibold">在线客户端</h2>
            <span className="rounded-full bg-slate-800 px-3 py-1 text-xs text-slate-300">{clients.length} 在线</span>
          </div>
          <div className="mt-4 overflow-x-auto">
            <table className="min-w-full text-left text-sm">
              <thead className="text-slate-400">
                <tr className="border-b border-slate-800">
                  <th className="py-3 pr-4">客户端</th>
                  <th className="py-3 pr-4">认证用户</th>
                  <th className="py-3 pr-4">TUN</th>
                  <th className="py-3 pr-4">运行时长</th>
                  <th className="py-3 pr-4">入站</th>
                  <th className="py-3 pr-4">出站</th>
                  <th className="py-3 pr-4">错误</th>
                </tr>
              </thead>
              <tbody>
                {clients.length === 0 ? (
                  <tr>
                    <td className="py-6 text-slate-500" colSpan={7}>
                      当前没有在线 VPN 客户端。
                    </td>
                  </tr>
                ) : (
                  clients.map((client) => (
                    <tr className="border-b border-slate-900" key={client.id}>
                      <td className="py-3 pr-4">{client.remote_addr || client.id}</td>
                      <td className="py-3 pr-4">{client.authenticated_as || '-'}</td>
                      <td className="py-3 pr-4">{client.tun_name || '-'}</td>
                      <td className="py-3 pr-4">{formatDuration(client.uptime_seconds)}</td>
                      <td className="py-3 pr-4">{formatBytes(client.bytes_in)}</td>
                      <td className="py-3 pr-4">{formatBytes(client.bytes_out)}</td>
                      <td className="py-3 pr-4 text-rose-300">{client.last_error || '-'}</td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </section>
      </main>
    </div>
  )
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: 'include',
    ...init,
  })
  if (!response.ok) {
    throw new APIError(response.status, await response.text())
  }
  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

class APIError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message || `HTTP ${status}`)
  }
}

function Info({ label, value, valueClass = 'text-slate-100' }: { label: string; value: string | number; valueClass?: string }) {
  return (
    <div className="rounded-lg bg-slate-950/70 p-3">
      <p className="text-xs text-slate-500">{label}</p>
      <p className={`mt-1 break-all ${valueClass}`}>{value}</p>
    </div>
  )
}

function LinkRow({ label, value, onCopy }: { label: string; value: string; onCopy: (value: string) => Promise<void> }) {
  const [copied, setCopied] = useState(false)

  async function handleCopy() {
    await onCopy(value)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="rounded-lg bg-slate-950/70 p-3">
      <p className="text-xs text-slate-500">{label}</p>
      <p className="mt-1 break-all text-slate-100">{value}</p>
      <button className="btn-secondary mt-3" onClick={() => void handleCopy()} type="button">
        {copied ? '已复制' : '复制链接'}
      </button>
    </div>
  )
}

async function copyToClipboard(text: string) {
  if (navigator.clipboard?.writeText && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', 'true')
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  document.body.appendChild(textarea)
  textarea.select()
  textarea.setSelectionRange(0, text.length)
  const ok = document.execCommand('copy')
  document.body.removeChild(textarea)
  if (!ok) {
    throw new Error('复制失败，请手动选中链接后复制')
  }
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

function errorMessage(err: unknown) {
  if (err instanceof APIError && err.status === 401) {
    return '账号或密码错误，或登录已过期。'
  }
  if (err instanceof Error) {
    return err.message
  }
  return String(err)
}

export default App
