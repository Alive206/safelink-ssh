import { useState, useEffect, useRef } from 'react'
import type { TunnelCfg, KeyInfo } from '../api/client'
import { keys as keysApi } from '../api/client'

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

  useEffect(() => { if (initial) setT(initial) }, [initial])

  // Load uploaded keys once on mount so the dropdown is populated.
  useEffect(() => { void refreshKeys() }, [])

  async function refreshKeys() {
    try {
      const list = await keysApi.list()
      setKeys(list)
      setKeysErr(null)
    } catch (e) {
      setKeysErr(e instanceof Error ? e.message : 'load keys failed')
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
      setKeysErr(err instanceof Error ? err.message : 'upload failed')
    } finally {
      setUploading(false)
    }
  }

  function up<K extends keyof TunnelCfg>(k: K, v: TunnelCfg[K]) { setT((p) => ({ ...p, [k]: v })) }
  function upSSH<K extends keyof TunnelCfg['ssh']>(k: K, v: TunnelCfg['ssh'][K]) {
    setT((p) => ({ ...p, ssh: { ...p.ssh, [k]: v } }))
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      // Strip masking placeholders so we don't accidentally write "***"
      // back to the server on edit.
      const clean: TunnelCfg = {
        ...t,
        ssh: {
          ...t.ssh,
          password: t.ssh.password === '***' ? '' : t.ssh.password,
          passphrase: t.ssh.passphrase === '***' ? '' : t.ssh.passphrase,
        },
      }
      await onSubmit(clean)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'save failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="bg-white rounded-lg shadow-sm ring-1 ring-slate-200 p-5 space-y-4">
      <h2 className="text-lg font-semibold">{isEdit ? 'Edit tunnel' : 'Add tunnel'}</h2>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Field label="Name">
          <input className={input} value={t.name} disabled={isEdit}
            onChange={(e) => up('name', e.target.value)} required />
        </Field>
        <Field label="Mode">
          <select className={input} value={t.mode}
            onChange={(e) => up('mode', e.target.value as TunnelCfg['mode'])}>
            <option value="local">local (-L)</option>
            <option value="remote">remote (-R)</option>
            <option value="dynamic">dynamic (-D, SOCKS5)</option>
          </select>
        </Field>
        <Field label="Listen">
          <input className={input} value={t.listen} placeholder="127.0.0.1:5433"
            onChange={(e) => up('listen', e.target.value)} required />
        </Field>
        <Field label="Forward" hint="ignored for dynamic">
          <input className={input} value={t.forward ?? ''} placeholder="db.internal:5432"
            disabled={t.mode === 'dynamic'}
            onChange={(e) => up('forward', e.target.value)} />
        </Field>
      </div>

      <h3 className="text-sm font-semibold text-slate-700 pt-2 border-t border-slate-200">SSH server</h3>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Field label="Address">
          <input className={input} value={t.ssh.addr} placeholder="jump.example.com:22"
            onChange={(e) => upSSH('addr', e.target.value)} required />
        </Field>
        <Field label="User">
          <input className={input} value={t.ssh.user}
            onChange={(e) => upSSH('user', e.target.value)} required />
        </Field>
        <Field label="Identity file" hint="upload an SSH private key; the daemon stores it under configs/keys with 0600 perms">
          <div className="flex gap-2">
            <select
              className={input}
              value={t.ssh.identity_file ?? ''}
              onChange={(e) => upSSH('identity_file', e.target.value)}
            >
              <option value="">— none (use password) —</option>
              {/* If the tunnel was edited and references a path that's no longer
                  in the keys directory, surface it so the user can see/clear it. */}
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
              title="Upload a private key file"
            >
              {uploading ? 'Uploading…' : 'Upload'}
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
        <Field label="Passphrase">
          <input className={input} type="password" value={t.ssh.passphrase ?? ''}
            onChange={(e) => upSSH('passphrase', e.target.value)} />
        </Field>
        <Field label="Password" hint="leave blank if using identity">
          <input className={input} type="password" value={t.ssh.password ?? ''}
            onChange={(e) => upSSH('password', e.target.value)} />
        </Field>
      </div>

      {err && <div className="text-sm text-rose-600">{err}</div>}

      <div className="flex justify-end gap-2 pt-2">
        <button type="button" onClick={onCancel}
          className="px-3 py-1.5 rounded ring-1 ring-slate-300 text-sm hover:bg-slate-50">Cancel</button>
        <button type="submit" disabled={busy}
          className="px-3 py-1.5 rounded bg-slate-900 text-white text-sm hover:bg-slate-800 disabled:opacity-50">
          {busy ? 'Saving…' : isEdit ? 'Save' : 'Create'}
        </button>
      </div>
    </form>
  )
}

const input = 'w-full rounded ring-1 ring-slate-300 focus:ring-slate-500 outline-none px-2.5 py-1.5 text-sm bg-white disabled:bg-slate-50'

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-slate-600">{label}</span>
      {children}
      {hint && <span className="block text-xs text-slate-400">{hint}</span>}
    </label>
  )
}
