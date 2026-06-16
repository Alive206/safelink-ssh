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

// ----- domain types -----

export interface SSHCfg {
  addr: string
  user: string
  identity_file?: string
  passphrase?: string
  password?: string
}

export interface TunnelCfg {
  name: string
  mode: 'local' | 'remote' | 'dynamic'
  listen: string
  forward?: string
  ssh: SSHCfg
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
