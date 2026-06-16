interface Props {
  state: string
}

const palette: Record<string, string> = {
  running: 'bg-emerald-100 text-emerald-700 ring-emerald-300',
  connecting: 'bg-amber-100 text-amber-700 ring-amber-300',
  reconnecting: 'bg-amber-100 text-amber-700 ring-amber-300',
  stopped: 'bg-slate-200 text-slate-600 ring-slate-300',
}

export default function StatusBadge({ state }: Props) {
  const cls = palette[state] ?? 'bg-rose-100 text-rose-700 ring-rose-300'
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ring-1 ring-inset ${cls}`}>
      {state}
    </span>
  )
}
