// Lightweight fetch wrapper.  All requests carry the session cookie
// automatically; on 401 we reject so callers can redirect to login.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'include',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: any = null
  if (text) {
    try { data = JSON.parse(text) } catch { data = text }
  }
  if (!res.ok) {
    const msg = (data && data.error) || res.statusText
    throw new ApiError(res.status, msg)
  }
  return data as T
}

export const api = {
  get: <T>(p: string) => request<T>('GET', p),
  post: <T>(p: string, b?: unknown) => request<T>('POST', p, b),
  put: <T>(p: string, b?: unknown) => request<T>('PUT', p, b),
  del: <T>(p: string) => request<T>('DELETE', p),
}

// ----- role -----

export type AppRole = 'server' | 'client' | 'standalone'

export interface RoleInfo {
  role: AppRole
}

export const appRole = {
  get: () => api.get<RoleInfo>('/api/role'),
}

// ----- domain types -----

export interface SSHCfg {
  addr: string
  user: string
  identity_file?: string
  passphrase?: string
  password?: string
}

export interface TunCfg {
  subnet: string
  dns?: string[]
  auto_route?: boolean
}

export interface TunnelCfg {
  name: string
  mode: 'local' | 'remote' | 'dynamic' | 'vpn'
  listen: string
  forward?: string
  ssh: SSHCfg
  tun?: TunCfg
}

export interface Snapshot {
  bytes_in: number
  bytes_out: number
  conn_active: number
  conn_total: number
}

export interface TunnelStatus {
  config: TunnelCfg
  state: 'connecting' | 'running' | 'reconnecting' | 'stopped' | string
  last_error?: string
  started_at?: string
  uptime_seconds: number
  run_count: number
  stats: Snapshot
  route_active?: boolean
}

export interface AuthInfo {
  auth_required: boolean
  login_enabled: boolean
}

export const tunnels = {
  list: () => api.get<TunnelStatus[]>('/api/tunnels'),
  get: (name: string) => api.get<TunnelStatus>(`/api/tunnels/${encodeURIComponent(name)}`),
  create: (t: TunnelCfg) => api.post<TunnelStatus>('/api/tunnels', t),
  update: (name: string, t: TunnelCfg) =>
    api.put<TunnelStatus>(`/api/tunnels/${encodeURIComponent(name)}`, t),
  remove: (name: string) => api.del<void>(`/api/tunnels/${encodeURIComponent(name)}`),
  start: (name: string) => api.post<void>(`/api/tunnels/${encodeURIComponent(name)}/start`),
  stop: (name: string) => api.post<void>(`/api/tunnels/${encodeURIComponent(name)}/stop`),
  restart: (name: string) => api.post<void>(`/api/tunnels/${encodeURIComponent(name)}/restart`),
  setRoute: (name: string, enable: boolean) => api.post<{ ok: boolean; route_active: boolean }>(`/api/tunnels/${encodeURIComponent(name)}/route`, { enable }),
}

export const auth = {
  info: () => api.get<AuthInfo>('/api/auth-info'),
  login: (username: string, password: string) =>
    api.post<{ ok: boolean }>('/api/login', { username, password }),
  logout: () => api.post<{ ok: boolean }>('/api/logout'),
}

// ----- ssh keys -----

export interface KeyInfo {
  name: string
  path: string
  size: number
  fingerprint: string
  has_password: boolean
  in_use: boolean
}

export const keys = {
  list: () => api.get<KeyInfo[]>('/api/keys'),
  remove: (name: string) => api.del<void>(`/api/keys/${encodeURIComponent(name)}`),
  // upload uses multipart/form-data; cannot reuse `request` helper.
  async upload(file: File, name?: string): Promise<KeyInfo> {
    const fd = new FormData()
    fd.append('file', file)
    if (name) fd.append('name', name)
    const res = await fetch('/api/keys', {
      method: 'POST',
      credentials: 'include',
      body: fd,
    })
    const text = await res.text()
    let data: any = null
    if (text) { try { data = JSON.parse(text) } catch { data = text } }
    if (!res.ok) {
      const msg = (data && data.error) || res.statusText
      throw new ApiError(res.status, msg)
    }
    return data as KeyInfo
  },
}

// ----- vpn driver -----

export interface DriverStatus {
  os: string
  installed: boolean
  driver_path?: string
  message: string
  can_auto_fix: boolean
}

export const vpn = {
  checkDriver: () => api.get<DriverStatus>('/api/vpn/driver'),
  installDriver: () => api.post<DriverStatus>('/api/vpn/driver/install'),
}

// ----- vpn deploy -----

export interface VPSDeployParams {
  ssh: SSHCfg
  subnet: string
  vpn_user: string
  vpn_pass: string
  local_name?: string
  force?: boolean
}

export interface VPSDeployResult {
  server_addr: string
  server_port: string
  subnet: string
  vpn_user: string
  vpn_pass: string
  egress_iface: string
  status: string
  build_method?: string
  error_message?: string
  tunnel_name?: string
}

export const deploy = {
  toVPS: (params: VPSDeployParams) => api.post<VPSDeployResult>('/api/vpn/deploy', params),
}

// ----- vpn servers -----

export interface VPNServer {
  id: string
  name: string
  server_addr: string
  server_port: string
  subnet: string
  vpn_user: string
  vpn_pass: string
  ssh_addr?: string
  ssh_user?: string
  ssh_password?: string
  egress_iface?: string
  status: string
  created_at: string
}

export const vpnServers = {
  list: () => api.get<VPNServer[]>('/api/vpn/servers'),
  add: (srv: Partial<VPNServer>) => api.post<VPNServer>('/api/vpn/servers', srv),
  remove: (id: string) => api.del<void>(`/api/vpn/servers/${encodeURIComponent(id)}`),
}

// ----- subscription -----

export interface SubscriptionToken {
  token: string
  url: string
}

export interface SubscriptionSource {
  id: string
  name: string
  url: string
  format: 'auto' | 'json' | 'clash'
  auto_refresh: boolean
  interval_min: number
  last_refresh?: string
  last_error?: string
  tunnel_count: number
}

export interface ImportResult {
  imported: number
  skipped: number
  errors: string[]
  nodes?: NodeInfo[]
}

export interface NodeInfo {
  name: string
  mode: string
  address: string
}

export const subscription = {
  getToken: () => api.get<SubscriptionToken>('/api/subscription/token'),
  regenerateToken: () => api.post<SubscriptionToken>('/api/subscription/token/regenerate'),
  getNodes: () => api.get<NodeInfo[]>('/api/subscription/nodes'),
  listImports: () => api.get<SubscriptionSource[]>('/api/subscription/imports'),
  addImport: (src: { name: string; url: string; format?: string; auto_refresh?: boolean; interval_min?: number }) =>
    api.post<SubscriptionSource>('/api/subscription/imports', src),
  removeImport: (id: string) => api.del<void>(`/api/subscription/imports/${encodeURIComponent(id)}`),
  refreshImport: (id: string) => api.post<ImportResult>(`/api/subscription/imports/${encodeURIComponent(id)}/refresh`),
}
