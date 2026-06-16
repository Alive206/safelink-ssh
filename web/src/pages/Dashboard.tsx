import { useEffect, useState } from 'react'
import { tunnels, TunnelStatus, TunnelCfg } from '../api/client'
import StatusBadge from '../components/StatusBadge'
import StatsCard, { formatBytes } from '../components/StatsCard'
import TunnelForm from './TunnelForm'

interface Props {
  onLogout: () => void
  onShowLogs: () => void
}

export default function Dashboard({ onLogout, onShowLogs }: Props) {
  const [list, setList] = useState<TunnelStatus[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [editing, setEditing] = useState<null | { mode: 'create' } | { mode: 'edit'; t: TunnelCfg }>(null)

  async function refresh() {
    try {
      setList(await tunnels.list())
      setErr(null)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'load failed')
    }
  }

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 2000)
    return () => clearInterval(t)
  }, [])

  // Aggregated traffic across all tunnels.
  const totals = list.reduce(
    (acc, s) => {
      acc.in += s.stats.bytes_in
      acc.out += s.stats.bytes_out
      acc.active += s.stats.conn_active
      acc.total += s.stats.conn_total
      return acc
    },
    { in: 0, out: 0, active: 0, total: 0 },
  )

  async function action(name: string, fn: () => Promise<unknown>) {
    try { await fn() } catch (e) {
      setErr(e instanceof Error ? e.message : 'action failed')
    } finally { refresh() }
  }

  async function save(t: TunnelCfg) {
    if (editing?.mode === 'edit') {
      await tunnels.update(editing.t.name, t)
    } else {
      await tunnels.create(t)
    }
    setEditing(null)
    refresh()
  }

  async function remove(name: string) {
    if (!confirm(`Delete tunnel "${name}"?`)) return
    await action(name, () => tunnels.remove(name))
  }

  return (
    <div className="min-h-full">
      <header className="bg-white border-b border-slate-200">
        <div className="max-w-6xl mx-auto px-6 py-3 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-slate-900 text-white text-xs font-bold">SL</span>
            <span className="text-lg font-semibold">SafeLink</span>
            <span className="text-xs text-slate-500">control panel</span>
          </div>
          <div className="flex items-center gap-2">
            <button onClick={onShowLogs}
              className="px-3 py-1.5 rounded ring-1 ring-slate-300 text-sm hover:bg-slate-50">Logs</button>
            <button onClick={onLogout}
              className="px-3 py-1.5 rounded ring-1 ring-slate-300 text-sm hover:bg-slate-50">Sign out</button>
          </div>
        </div>
      </header>

      <main className="max-w-6xl mx-auto px-6 py-6 space-y-6">
        {err && (
          <div className="bg-rose-50 ring-1 ring-rose-200 text-rose-700 text-sm rounded p-3">{err}</div>
        )}

        <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <StatsCard label="Tunnels" value={list.length} />
          <StatsCard label="Active conns" value={totals.active} hint={`${totals.total} since start`} />
          <StatsCard label="Bytes in"   value={formatBytes(totals.in)} />
          <StatsCard label="Bytes out"  value={formatBytes(totals.out)} />
        </section>

        <section className="bg-white rounded-lg shadow-sm ring-1 ring-slate-200 overflow-hidden">
          <div className="flex items-center justify-between px-4 py-3 border-b border-slate-200">
            <h2 className="font-semibold">Tunnels</h2>
            <button onClick={() => setEditing({ mode: 'create' })}
              className="px-3 py-1.5 rounded bg-slate-900 text-white text-sm hover:bg-slate-800">+ Add tunnel</button>
          </div>
          <table className="w-full text-sm">
            <thead className="bg-slate-50 text-slate-600 text-xs uppercase">
              <tr>
                <Th>Name</Th><Th>Mode</Th><Th>Listen → Forward</Th><Th>State</Th>
                <Th>Conns</Th><Th>Traffic</Th><Th>Uptime</Th><Th className="text-right">Actions</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {list.length === 0 && (
                <tr><td colSpan={8} className="px-4 py-8 text-center text-slate-500">No tunnels configured.</td></tr>
              )}
              {list.map((s) => (
                <tr key={s.config.name}>
                  <Td><span className="font-medium">{s.config.name}</span></Td>
                  <Td><span className="text-slate-600">{s.config.mode}</span></Td>
                  <Td className="font-mono text-xs">
                    {s.config.listen}
                    {s.config.mode !== 'dynamic' && s.config.forward ? ` → ${s.config.forward}` : ''}
                  </Td>
                  <Td>
                    <div className="flex flex-col gap-1">
                      <StatusBadge state={s.state} />
                      {s.last_error && <span className="text-xs text-rose-600 truncate max-w-[18rem]" title={s.last_error}>{s.last_error}</span>}
                    </div>
                  </Td>
                  <Td>{s.stats.conn_active}<span className="text-slate-400"> / {s.stats.conn_total}</span></Td>
                  <Td className="text-xs">
                    <div>↓ {formatBytes(s.stats.bytes_in)}</div>
                    <div>↑ {formatBytes(s.stats.bytes_out)}</div>
                  </Td>
                  <Td className="text-xs">
                    {s.state === 'running' ? `${s.uptime_seconds}s` : '—'}
                    <div className="text-slate-400">{s.run_count} runs</div>
                  </Td>
                  <Td>
                    <div className="flex justify-end gap-1">
                      {s.state === 'stopped' ? (
                        <Btn onClick={() => action(s.config.name, () => tunnels.start(s.config.name))}>Start</Btn>
                      ) : (
                        <Btn onClick={() => action(s.config.name, () => tunnels.stop(s.config.name))}>Stop</Btn>
                      )}
                      <Btn onClick={() => action(s.config.name, () => tunnels.restart(s.config.name))}>Restart</Btn>
                      <Btn onClick={() => setEditing({ mode: 'edit', t: s.config })}>Edit</Btn>
                      <Btn danger onClick={() => remove(s.config.name)}>Delete</Btn>
                    </div>
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>

        {editing && (
          <section>
            <TunnelForm
              isEdit={editing.mode === 'edit'}
              initial={editing.mode === 'edit' ? editing.t : undefined}
              onSubmit={save}
              onCancel={() => setEditing(null)}
            />
          </section>
        )}
      </main>
    </div>
  )
}

function Th({ children, className = '' }: { children: React.ReactNode; className?: string }) {
  return <th className={`text-left font-medium px-4 py-2 ${className}`}>{children}</th>
}
function Td({ children, className = '' }: { children: React.ReactNode; className?: string }) {
  return <td className={`px-4 py-2 align-top ${className}`}>{children}</td>
}
function Btn({ children, onClick, danger }: { children: React.ReactNode; onClick: () => void; danger?: boolean }) {
  const cls = danger
    ? 'ring-rose-300 text-rose-700 hover:bg-rose-50'
    : 'ring-slate-300 text-slate-700 hover:bg-slate-50'
  return (
    <button onClick={onClick} className={`px-2 py-1 rounded ring-1 text-xs ${cls}`}>{children}</button>
  )
}
