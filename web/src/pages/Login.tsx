import { useState } from 'react'
import { auth, ApiError } from '../api/client'

interface Props {
  onSuccess: () => void
}

export default function Login({ onSuccess }: Props) {
  const [u, setU] = useState('')
  const [p, setP] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      await auth.login(u, p)
      onSuccess()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : 'login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-full flex items-center justify-center p-6">
      <form onSubmit={submit} className="w-full max-w-sm bg-white rounded-lg shadow ring-1 ring-slate-200 p-6 space-y-4">
        <div className="flex items-center gap-2">
          <span className="inline-flex h-8 w-8 items-center justify-center rounded-md bg-slate-900 text-white text-sm font-bold">SL</span>
          <h1 className="text-xl font-semibold">SafeLink</h1>
        </div>
        <p className="text-sm text-slate-500">Sign in to manage your secure tunnels.</p>
        <div className="space-y-1">
          <label className="text-sm font-medium">Username</label>
          <input
            className="w-full rounded border-slate-300 ring-1 ring-slate-300 focus:ring-slate-500 px-3 py-2 outline-none"
            value={u} onChange={(e) => setU(e.target.value)} autoFocus
          />
        </div>
        <div className="space-y-1">
          <label className="text-sm font-medium">Password</label>
          <input
            type="password"
            className="w-full rounded border-slate-300 ring-1 ring-slate-300 focus:ring-slate-500 px-3 py-2 outline-none"
            value={p} onChange={(e) => setP(e.target.value)}
          />
        </div>
        {err && <div className="text-sm text-rose-600">{err}</div>}
        <button
          type="submit"
          disabled={busy || !u || !p}
          className="w-full rounded bg-slate-900 text-white py-2 font-medium hover:bg-slate-800 disabled:opacity-50"
        >
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
