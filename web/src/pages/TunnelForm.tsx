import { useState, useEffect, useRef } from 'react'
import type { TunnelCfg, KeyInfo, DriverStatus, VPSDeployResult, VPNServer } from '../api/client'
import { keys as keysApi, vpn as vpnApi, deploy as deployApi, vpnServers as vpnServersApi } from '../api/client'

interface Props {
  initial?: TunnelCfg
  isEdit?: boolean
  onSubmit: (t: TunnelCfg) => Promise<void> | void
  onCancel: () => void
}

const empty: TunnelCfg = {
  name: '',
  mode: 'local',
  listen: '',
  forward: '',
  ssh: { addr: '', user: '', identity_file: '', passphrase: '', password: '' },
}

export default function TunnelForm({ initial, isEdit, onSubmit, onCancel }: Props) {
  const [t, setT] = useState<TunnelCfg>(initial ?? empty)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [keys, setKeys] = useState<KeyInfo[]>([])
  const [keysErr, setKeysErr] = useState<string | null>(null)
  const [uploading, setUploading] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const isVPN = t.mode === 'vpn'

  // Driver state for VPN mode.
  const [driver, setDriver] = useState<DriverStatus | null>(null)
  const [driverBusy, setDriverBusy] = useState(false)

  // Deploy state for one-click VPS deployment.
  const [deploySSH, setDeploySSH] = useState<{ addr: string; user: string; password: string }>({ addr: '', user: 'root', password: '' })
  const [deploySubnet, setDeploySubnet] = useState('10.0.8.0/24')
  const [deployBusy, setDeployBusy] = useState(false)
  const [deployResult, setDeployResult] = useState<VPSDeployResult | null>(null)
  const [deployErr, setDeployErr] = useState<string | null>(null)

  // VPN servers state.
  const [servers, setServers] = useState<VPNServer[]>([])
  const [showAddServer, setShowAddServer] = useState(false)
  const [newServer, setNewServer] = useState<Partial<VPNServer>>({ server_addr: '', server_port: '1562', subnet: '10.0.8.0/24', vpn_user: 'vpn', vpn_pass: '' })
  const [addServerBusy, setAddServerBusy] = useState(false)

  useEffect(() => { if (initial) setT(initial) }, [initial])

  // Load uploaded keys once on mount so the dropdown is populated.
  useEffect(() => { void refreshKeys() }, [])

  // Check TUN driver when VPN mode is selected.
  useEffect(() => {
    if (isVPN) {
      vpnApi.checkDriver().then(setDriver).catch(() => setDriver(null))
      vpnServersApi.list().then(setServers).catch(() => setServers([]))
    }
  }, [isVPN])

  async function refreshKeys() {
    try {
      const list = await keysApi.list()
      setKeys(list)
      setKeysErr(null)
    } catch (e) {
      setKeysErr(e instanceof Error ? e.message : '加载密钥失败')
    }
  }

  async function onPickFile(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0]
    e.target.value = ''
    if (!f) return
    setUploading(true)
    setKeysErr(null)
    try {
      const info = await keysApi.upload(f, f.name)
      await refreshKeys()
      // Auto-select the freshly uploaded key.
      upSSH('identity_file', info.path)
    } catch (err) {
      setKeysErr(err instanceof Error ? err.message : '上传失败')
    } finally {
      setUploading(false)
    }
  }

  function up<K extends keyof TunnelCfg>(k: K, v: TunnelCfg[K]) { setT((p) => ({ ...p, [k]: v })) }
  function upSSH<K extends keyof TunnelCfg['ssh']>(k: K, v: TunnelCfg['ssh'][K]) {
    setT((p) => ({ ...p, ssh: { ...p.ssh, [k]: v } }))
  }

  async function handleDeploy(force?: boolean) {
    setDeployBusy(true)
    setDeployErr(null)
    setDeployResult(null)
    try {
      const res = await deployApi.toVPS({
        ssh: { addr: deploySSH.addr, user: deploySSH.user, password: deploySSH.password, identity_file: '', passphrase: '' },
        subnet: deploySubnet,
        vpn_user: t.ssh.user || 'vpn',
        vpn_pass: t.ssh.password || '',
        local_name: t.name || undefined,
        force: force || undefined,
      })
      setDeployResult(res)
      // Auto-fill the tunnel form fields.
      up('forward', `${res.server_addr}:${res.server_port}`)
      const clientIP = res.subnet.replace('.1/', '.2/')
      up('listen', clientIP)
      upSSH('user', res.vpn_user)
      if (res.vpn_pass) upSSH('password', res.vpn_pass)
      // Refresh server list after deploy.
      vpnServersApi.list().then(setServers).catch(() => {})
    } catch (e) {
      setDeployErr(e instanceof Error ? e.message : '部署失败')
    } finally {
      setDeployBusy(false)
    }
  }

  function selectServer(srv: VPNServer) {
    up('forward', `${srv.server_addr}:${srv.server_port}`)
    const clientIP = srv.subnet.replace('.0/', '.2/').replace('.1/', '.2/')
    up('listen', clientIP)
    upSSH('user', srv.vpn_user)
    upSSH('password', srv.vpn_pass)
    setT(prev => ({ ...prev, tun: { ...prev.tun, subnet: clientIP, auto_route: prev.tun?.auto_route ?? true } }))
  }

  async function handleAddServer() {
    setAddServerBusy(true)
    try {
      await vpnServersApi.add(newServer)
      const list = await vpnServersApi.list()
      setServers(list)
      setShowAddServer(false)
      setNewServer({ server_addr: '', server_port: '1562', subnet: '10.0.8.0/24', vpn_user: 'vpn', vpn_pass: '' })
    } catch (e) {
      setErr(e instanceof Error ? e.message : '添加服务器失败')
    } finally {
      setAddServerBusy(false)
    }
  }

  async function handleDeleteServer(id: string) {
    try {
      await vpnServersApi.remove(id)
      setServers(prev => prev.filter(s => s.id !== id))
    } catch (e) {
      setErr(e instanceof Error ? e.message : '删除失败')
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await onSubmit(t)
    } catch (e) {
      setErr(e instanceof Error ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="bg-white rounded-lg shadow-sm ring-1 ring-slate-200 p-5 space-y-4">
      <h2 className="text-lg font-semibold">{isEdit ? '编辑隧道' : '添加隧道'}</h2>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Field label="名称">
          <input className={input} value={t.name} disabled={isEdit}
            onChange={(e) => up('name', e.target.value)} required />
        </Field>
        <Field label="模式">
          <select className={input} value={t.mode}
            onChange={(e) => up('mode', e.target.value as TunnelCfg['mode'])}>
            <option value="local">本地转发 (-L)</option>
            <option value="remote">远程转发 (-R)</option>
            <option value="dynamic">动态转发 (-D, SOCKS5)</option>
            <option value="vpn">VPN 隧道 (QUIC)</option>
          </select>
        </Field>
        {isVPN ? (
          <Field label="客户端地址" hint="本地 TUN 接口的 IP/子网，如 10.0.8.2/24">
            <input className={input} value={t.listen} placeholder="10.0.8.2/24"
              onChange={(e) => up('listen', e.target.value)} required />
          </Field>
        ) : (
          <Field label="监听地址">
            <input className={input} value={t.listen} placeholder="127.0.0.1:5433"
              onChange={(e) => up('listen', e.target.value)} required />
          </Field>
        )}
        {isVPN ? (
          <Field label="VPN 服务器" hint="远程 safelink server 的地址和端口，如 1.2.3.4:1562">
            <input className={input} value={t.forward ?? ''} placeholder="your-vps-ip:1562"
              onChange={(e) => up('forward', e.target.value)} required />
          </Field>
        ) : (
          <Field label="转发地址" hint="动态模式忽略此项">
            <input className={input} value={t.forward ?? ''} placeholder="db.internal:5432"
              disabled={t.mode === 'dynamic'}
              onChange={(e) => up('forward', e.target.value)} />
          </Field>
        )}
      </div>

      {isVPN ? (
        <>
          {/* TUN driver status */}
          {driver && (
            <div className={`rounded p-3 text-sm space-y-2 ${driver.installed ? 'bg-emerald-50 ring-1 ring-emerald-200' : 'bg-amber-50 ring-1 ring-amber-200'}`}>
              <div className="flex items-center justify-between">
                <div>
                  <span className="font-medium">{driver.installed ? '✅ TUN 驱动已就绪' : '⚠️ 需要 TUN 驱动'}</span>
                  <p className="text-xs mt-0.5">{driver.message}</p>
                </div>
                {!driver.installed && driver.can_auto_fix && (
                  <button
                    type="button"
                    disabled={driverBusy}
                    onClick={async () => {
                      setDriverBusy(true)
                      try {
                        const st = await vpnApi.installDriver()
                        setDriver(st)
                      } catch (e) {
                        setErr(e instanceof Error ? e.message : '驱动安装失败')
                      } finally {
                        setDriverBusy(false)
                      }
                    }}
                    className="shrink-0 px-3 py-1.5 rounded bg-slate-900 text-white text-xs hover:bg-slate-800 disabled:opacity-50"
                  >
                    {driverBusy ? '安装中…' : '一键安装'}
                  </button>
                )}
              </div>
              {!driver.installed && !driver.can_auto_fix && driver.os !== 'windows' && (
                <p className="text-xs">Linux/macOS 请以 root 权限运行，TUN 模块已内置</p>
              )}
            </div>
          )}

          <h3 className="text-sm font-semibold text-slate-700 pt-2 border-t border-slate-200">选择 VPN 服务器</h3>
          {servers.length > 0 ? (
            <div className="space-y-2">
              <div className="grid grid-cols-1 gap-2 max-h-40 overflow-y-auto">
                {servers.map(srv => (
                  <div key={srv.id} className="flex items-center justify-between p-2 rounded ring-1 ring-slate-200 bg-slate-50 text-sm">
                    <div className="flex-1 min-w-0">
                      <span className="font-medium">{srv.name}</span>
                      <span className="text-slate-400 ml-2">{srv.server_addr}:{srv.server_port}</span>
                      <span className={`ml-2 text-xs px-1.5 py-0.5 rounded ${srv.status === 'running' ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-200 text-slate-600'}`}>{srv.status}</span>
                    </div>
                    <div className="flex gap-1 shrink-0 ml-2">
                      <button type="button" onClick={() => selectServer(srv)}
                        className="px-2 py-1 text-xs rounded bg-indigo-600 text-white hover:bg-indigo-700">使用</button>
                      <button type="button" onClick={() => handleDeleteServer(srv.id)}
                        className="px-2 py-1 text-xs rounded ring-1 ring-slate-300 hover:bg-rose-50 hover:text-rose-600">删除</button>
                    </div>
                  </div>
                ))}
              </div>
              <button type="button" onClick={() => setShowAddServer(!showAddServer)}
                className="text-xs text-indigo-600 hover:underline">
                {showAddServer ? '取消' : '+ 手动添加服务器'}
              </button>
            </div>
          ) : (
            <div className="text-sm text-slate-500">
              暂无可用服务器。可通过下方部署或
              <button type="button" onClick={() => setShowAddServer(true)} className="text-indigo-600 hover:underline ml-1">手动添加</button>
            </div>
          )}

          {showAddServer && (
            <div className="bg-slate-50 ring-1 ring-slate-200 rounded p-3 space-y-2">
              <h4 className="text-xs font-semibold text-slate-600">手动添加 VPN 服务器</h4>
              <div className="grid grid-cols-2 gap-2">
                <input className={input2} placeholder="服务器地址 (如 1.2.3.4)" value={newServer.server_addr || ''}
                  onChange={e => setNewServer(p => ({ ...p, server_addr: e.target.value }))} />
                <input className={input2} placeholder="端口 (默认 1562)" value={newServer.server_port || ''}
                  onChange={e => setNewServer(p => ({ ...p, server_port: e.target.value }))} />
                <input className={input2} placeholder="子网 (如 10.0.8.0/24)" value={newServer.subnet || ''}
                  onChange={e => setNewServer(p => ({ ...p, subnet: e.target.value }))} />
                <input className={input2} placeholder="VPN 用户名" value={newServer.vpn_user || ''}
                  onChange={e => setNewServer(p => ({ ...p, vpn_user: e.target.value }))} />
                <input className={input2} placeholder="VPN 密码" value={newServer.vpn_pass || ''}
                  onChange={e => setNewServer(p => ({ ...p, vpn_pass: e.target.value }))} />
                <input className={input2} placeholder="名称 (可选)" value={newServer.name || ''}
                  onChange={e => setNewServer(p => ({ ...p, name: e.target.value }))} />
              </div>
              <button type="button" disabled={addServerBusy || !newServer.server_addr} onClick={handleAddServer}
                className="px-3 py-1.5 rounded bg-slate-800 text-white text-xs hover:bg-slate-700 disabled:opacity-50">
                {addServerBusy ? '添加中...' : '保存服务器'}
              </button>
            </div>
          )}

          <h3 className="text-sm font-semibold text-slate-700 pt-2 border-t border-slate-200">VPN 认证</h3>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <Field label="用户名">
              <input className={input} value={t.ssh.user}
                onChange={(e) => upSSH('user', e.target.value)} required />
            </Field>
            <Field label="密码" hint="与服务端 --pass 参数一致">
              <PwdInput value={t.ssh.password ?? ''}
                onChange={(v) => upSSH('password', v)} required />
            </Field>
          </div>
          {/* Deploy card */}
          <div className="bg-indigo-50 ring-1 ring-indigo-200 rounded p-4 text-sm space-y-3">
            <div className="flex items-center gap-2">
              <span className="text-base">🚀</span>
              <span className="font-semibold text-indigo-900">一键部署到远程服务器</span>
            </div>
            <p className="text-indigo-700 text-xs">填入 VPS 的 SSH 信息，自动上传 safelink、配置 NAT、启动 VPN 服务端</p>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
              <div>
                <label className="text-xs font-medium text-indigo-800">服务器地址</label>
                <input
                  className={input2} value={deploySSH.addr}
                  onChange={(e) => setDeploySSH((p) => ({ ...p, addr: e.target.value }))}
                  placeholder="your-vps-ip:22"
                />
              </div>
              <div>
                <label className="text-xs font-medium text-indigo-800">SSH 用户名</label>
                <input
                  className={input2} value={deploySSH.user}
                  onChange={(e) => setDeploySSH((p) => ({ ...p, user: e.target.value }))}
                  placeholder="root"
                />
              </div>
              <div>
                <label className="text-xs font-medium text-indigo-800">SSH 密码</label>
                <input
                  className={input2} value={deploySSH.password}
                  onChange={(e) => setDeploySSH((p) => ({ ...p, password: e.target.value }))}
                  placeholder="SSH password"
                />
              </div>
              <div>
                <label className="text-xs font-medium text-indigo-800">TUN 子网</label>
                <input
                  className={input2} value={deploySubnet}
                  onChange={(e) => setDeploySubnet(e.target.value)}
                  placeholder="10.0.8.0/24"
                />
              </div>
            </div>

            {deployErr && <div className="text-xs text-rose-600 bg-rose-50 rounded p-2">{deployErr}</div>}

            {deployResult ? (
              <div className={`rounded p-3 text-xs space-y-1 ${
                deployResult.build_method === 'existing'
                  ? 'bg-sky-50 ring-1 ring-sky-200 text-sky-800'
                  : 'bg-emerald-50 ring-1 ring-emerald-200 text-emerald-800'
              }`}>
                <p className="font-semibold">
                  {deployResult.build_method === 'existing' ? '✅ 服务器已在运行中' : '✅ 部署成功！'}
                </p>
                <p>服务器: {deployResult.server_addr}:{deployResult.server_port}</p>
                <p>子网: {deployResult.subnet}</p>
                <p>接口: {deployResult.egress_iface}</p>
                {deployResult.tunnel_name && <p>隧道: {deployResult.tunnel_name} 已自动创建</p>}
                <p className="text-emerald-600 mt-1">已自动填入下方 VPN 配置，保存后启动即可连接</p>
                {deployResult.build_method === 'existing' && (
                  <button
                    type="button"
                    disabled={deployBusy}
                    onClick={() => handleDeploy(true)}
                    className="mt-2 w-full px-3 py-1.5 rounded bg-amber-500 text-white text-xs font-medium hover:bg-amber-600 disabled:opacity-50"
                  >
                    {deployBusy ? '部署中...' : '重新部署（强制更新）'}
                  </button>
                )}
              </div>
            ) : (
              <button
                type="button"
                disabled={deployBusy || !deploySSH.addr || !deploySSH.password}
                onClick={() => handleDeploy()}
                className="w-full px-3 py-2 rounded bg-indigo-700 text-white text-sm font-medium hover:bg-indigo-800 disabled:opacity-50"
              >
                {deployBusy ? (
                  <span className="flex items-center justify-center gap-2">
                    <span className="animate-pulse">⏳</span>
                    <span>部署中...（上传二进制、配置防火墙、启动服务）</span>
                  </span>
                ) : '部署到服务器'}
              </button>
            )}
          </div>
          {/* Auto route toggle */}
          <label className="flex items-center gap-2 text-sm cursor-pointer select-none">
            <input
              type="checkbox"
              checked={t.mode === 'vpn' ? (t.tun?.auto_route ?? true) : false}
              onChange={(e) => setT((p) => ({
                ...p,
                tun: { ...p.tun, subnet: p.listen || p.tun?.subnet || '', auto_route: e.target.checked }
              }))}
              className="rounded border-slate-300"
            />
            <span className="font-medium">自动设置路由</span>
            <span className="text-slate-400">（连接时添加默认路由，断开时自动删除）</span>
          </label>
        </>
      ) : (
        <>
          <h3 className="text-sm font-semibold text-slate-700 pt-2 border-t border-slate-200">SSH 服务器</h3>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <Field label="服务器地址">
              <input className={input} value={t.ssh.addr} placeholder="jump.example.com:22"
                onChange={(e) => upSSH('addr', e.target.value)} required />
            </Field>
            <Field label="用户名">
              <input className={input} value={t.ssh.user}
                onChange={(e) => upSSH('user', e.target.value)} required />
            </Field>
            <Field label="密钥文件" hint="上传 SSH 私钥；守护进程以 0600 权限存储在 configs/keys 目录下">
              <div className="flex gap-2">
                <select
                  className={input}
                  value={t.ssh.identity_file ?? ''}
                  onChange={(e) => upSSH('identity_file', e.target.value)}
                >
                  <option value="">— 无（使用密码）—</option>
                  {t.ssh.identity_file && !keys.some(k => k.path === t.ssh.identity_file) && (
                    <option value={t.ssh.identity_file}>(custom) {t.ssh.identity_file}</option>
                  )}
                  {keys.map(k => (
                    <option key={k.name} value={k.path}>
                      {k.name}{k.fingerprint ? ` · ${k.fingerprint.slice(0, 19)}…` : ''}{k.has_password ? ' · 🔒' : ''}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  onClick={() => fileRef.current?.click()}
                  disabled={uploading}
                  className="shrink-0 px-2.5 py-1.5 rounded ring-1 ring-slate-300 text-xs hover:bg-slate-50 disabled:opacity-50"
                  title="上传私钥文件"
                >
                  {uploading ? '上传中…' : '上传'}
                </button>
                <input
                  ref={fileRef}
                  type="file"
                  accept=".pem,.key,.openssh,id_rsa,id_ed25519,id_ecdsa,id_dsa,*"
                  className="hidden"
                  onChange={onPickFile}
                />
              </div>
              {keysErr && <span className="block text-xs text-rose-600 mt-1">{keysErr}</span>}
            </Field>
            <Field label="密钥密码">
              <PwdInput value={t.ssh.passphrase ?? ''}
                onChange={(v) => upSSH('passphrase', v)} />
            </Field>
            <Field label="密码" hint="使用密钥时留空">
              <PwdInput value={t.ssh.password ?? ''}
                onChange={(v) => upSSH('password', v)} />
            </Field>
          </div>
        </>
      )}

      {err && <div className="text-sm text-rose-600">{err}</div>}

      <div className="flex justify-end gap-2 pt-2">
        <button type="button" onClick={onCancel}
          className="px-3 py-1.5 rounded ring-1 ring-slate-300 text-sm hover:bg-slate-50">取消</button>
        <button type="submit" disabled={busy}
          className="px-3 py-1.5 rounded bg-slate-900 text-white text-sm hover:bg-slate-800 disabled:opacity-50">
          {busy ? '保存中…' : isEdit ? '保存' : '创建'}
        </button>
      </div>
    </form>
  )
}

const input = 'w-full rounded ring-1 ring-slate-300 focus:ring-slate-500 outline-none px-2.5 py-1.5 text-sm bg-white disabled:bg-slate-50'
const input2 = 'w-full rounded ring-1 ring-indigo-300 focus:ring-indigo-500 outline-none px-2 py-1 text-xs bg-white disabled:bg-slate-50'

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-slate-600">{label}</span>
      {children}
      {hint && <span className="block text-xs text-slate-400">{hint}</span>}
    </label>
  )
}

/** Password input with eye toggle. Default shows plain text. */
function PwdInput({ className, value, onChange, placeholder, required }: {
  className?: string; value: string; onChange: (v: string) => void; placeholder?: string; required?: boolean
}) {
  const [visible, setVisible] = useState(true)
  return (
    <div className="relative">
      <input
        className={className || input}
        type={visible ? 'text' : 'password'}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        required={required}
      />
      <button
        type="button"
        onClick={() => setVisible(!visible)}
        className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600 text-xs select-none"
        tabIndex={-1}
      >
        {visible ? '🙈' : '👁'}
      </button>
    </div>
  )
}
