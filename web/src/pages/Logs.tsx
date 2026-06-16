import { useSSE } from '../hooks/useSSE'

interface Props {
  onClose: () => void
}

interface LogLine { time?: string; level?: string; msg?: string; [k: string]: any }

function parse(line: string): LogLine {
  try { return JSON.parse(line) } catch { return { msg: line } }
}

const levelClass: Record<string, string> = {
  DEBUG: 'text-slate-400',
  INFO: 'text-slate-700',
  WARN: 'text-amber-600',
  ERROR: 'text-rose-600',
}

export default function Logs({ onClose }: Props) {
  const lines = useSSE('/api/logs', 1000)
  return (
    <div className="bg-white rounded-lg shadow-sm ring-1 ring-slate-200 p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Live logs</h2>
        <button onClick={onClose}
          className="px-3 py-1.5 rounded ring-1 ring-slate-300 text-sm hover:bg-slate-50">Close</button>
      </div>
      <div className="font-mono text-xs bg-slate-900 text-slate-100 rounded p-3 h-[60vh] overflow-auto whitespace-pre-wrap">
        {lines.length === 0 && <div className="text-slate-400">Waiting for logs…</div>}
        {lines.map((raw, i) => {
          const l = parse(raw)
          const lvl = (l.level || 'INFO').toUpperCase()
          const cls = levelClass[lvl] ?? 'text-slate-200'
          const extras = Object.entries(l)
            .filter(([k]) => k !== 'time' && k !== 'level' && k !== 'msg')
            .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
            .join(' ')
          return (
            <div key={i} className="leading-5">
              <span className="text-slate-500">{l.time?.slice(11, 19) ?? ''} </span>
              <span className={cls}>{lvl.padEnd(5)} </span>
              <span className="text-slate-100">{l.msg}</span>
              {extras && <span className="text-slate-400"> {extras}</span>}
            </div>
          )
        })}
      </div>
    </div>
  )
}
