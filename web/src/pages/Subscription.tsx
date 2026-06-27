import { useEffect, useState } from 'react'
import { subscription, SubscriptionToken, SubscriptionSource, ImportResult, NodeInfo, AppRole } from '../api/client'

interface Props {
  role: AppRole
  onClose: () => void
}

export default function Subscription({ role, onClose }: Props) {
  const showPublish = role === 'server' || role === 'standalone'
  const showImport = role === 'client' || role === 'standalone'
  const defaultTab = showPublish ? 'publish' : 'import'

  const [tab, setTab] = useState<'publish' | 'import'>(defaultTab)
  const [token, setToken] = useState<SubscriptionToken | null>(null)
  const [sources, setSources] = useState<SubscriptionSource[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [copied, setCopied] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [refreshing, setRefreshing] = useState<string | null>(null)
  const [lastResult, setLastResult] = useState<ImportResult | null>(null)
  const [publishNodes, setPublishNodes] = useState<NodeInfo[]>([])

  // Add form state
  const [form, setForm] = useState({ name: '', url: '', format: 'auto', auto_refresh: false, interval_min: 60 })

  async function loadToken() {
    try {
      setToken(await subscription.getToken())
      setPublishNodes(await subscription.getNodes())
    } catch (e) {
      setErr(e instanceof Error ? e.message : '获取失败')
    }
  }

  async function loadSources() {
    try {
      setSources(await subscription.listImports())
    } catch (e) {
      setErr(e instanceof Error ? e.message : '获取失败')
    }
  }

  useEffect(() => {
    if (showPublish) loadToken()
    if (showImport) loadSources()
  }, [])

  async function regenerate() {
    if (!confirm('重新生成 Token 后，旧链接将立即失效，确认？')) return
    try {
      setToken(await subscription.regenerateToken())
    } catch (e) {
      setErr(e instanceof Error ? e.message : '操作失败')
    }
  }

  async function copyURL(url: string, label: string) {
    await navigator.clipboard.writeText(url)
    setCopied(label)
    setTimeout(() => setCopied(null), 2000)
  }

  async function addSource() {
    if (!form.name || !form.url) { setErr('名称和 URL 不能为空'); return }
    try {
      await subscription.addImport(form)
      setAdding(false)
      setForm({ name: '', url: '', format: 'auto', auto_refresh: false, interval_min: 60 })
      loadSources()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '添加失败')
    }
  }

  async function removeSource(id: string) {
    if (!confirm('确认删除该订阅源？')) return
    try {
      await subscription.removeImport(id)
      loadSources()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '删除失败')
    }
  }

  async function refreshSource(id: string) {
    setRefreshing(id)
    setLastResult(null)
    try {
      const result = await subscription.refreshImport(id)
      setLastResult(result)
      loadSources()
    } catch (e) {
      setErr(e instanceof Error ? e.message : '刷新失败')
    } finally {
      setRefreshing(null)
    }
  }

  function timeAgo(iso?: string) {
    if (!iso) return '从未'
    const diff = Date.now() - new Date(iso).getTime()
    if (diff < 60_000) return '刚刚'
    if (diff < 3600_000) return `${Math.floor(diff / 60_000)} 分钟前`
    if (diff < 86400_000) return `${Math.floor(diff / 3600_000)} 小时前`
    return `${Math.floor(diff / 86400_000)} 天前`
  }

  return (
    <div className="min-h-full">
      <header className="bg-white border-b border-slate-200">
        <div className="max-w-6xl mx-auto px-6 py-3 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-slate-900 text-white text-xs font-bold">SL</span>
            <span className="text-lg font-semibold">SafeLink</span>
            <span className="text-xs text-slate-500">订阅管理</span>
          </div>
          <button onClick={onClose}
            className="px-3 py-1.5 rounded ring-1 ring-slate-300 text-sm hover:bg-slate-50">返回</button>
        </div>
      </header>

      <main className="max-w-6xl mx-auto px-6 py-6 space-y-6">
        {err && (
          <div className="bg-rose-50 ring-1 ring-rose-200 text-rose-700 text-sm rounded p-3 flex justify-between items-center">
            <span>{err}</span>
            <button onClick={() => setErr(null)} className="text-rose-500 hover:text-rose-700 font-bold">×</button>
          </div>
        )}

        {/* Tab Switcher - only show when both tabs are available */}
        {showPublish && showImport && (
          <div className="flex gap-1 bg-slate-100 rounded-lg p-1 w-fit">
            <button
              onClick={() => setTab('publish')}
              className={`px-4 py-1.5 rounded text-sm font-medium transition ${tab === 'publish' ? 'bg-white shadow-sm text-slate-900' : 'text-slate-500 hover:text-slate-700'}`}
            >发布订阅</button>
            <button
              onClick={() => setTab('import')}
              className={`px-4 py-1.5 rounded text-sm font-medium transition ${tab === 'import' ? 'bg-white shadow-sm text-slate-900' : 'text-slate-500 hover:text-slate-700'}`}
            >导入订阅</button>
          </div>
        )}

        {/* Publish Tab */}
        {tab === 'publish' && showPublish && (
          <section className="bg-white rounded-lg shadow-sm ring-1 ring-slate-200 p-6 space-y-4">
            <h2 className="font-semibold text-lg">订阅链接</h2>
            <p className="text-sm text-slate-500">将以下链接分享给其他设备或客户端，即可导入本实例的隧道配置。</p>

            {token && (
              <div className="space-y-3">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-medium text-slate-500 w-20 shrink-0">JSON 格式</span>
                  <code className="flex-1 bg-slate-50 px-3 py-2 rounded text-xs font-mono border border-slate-200 truncate">
                    {token.url}
                  </code>
                  <button onClick={() => copyURL(token.url, 'json')}
                    className="px-3 py-1.5 rounded bg-slate-900 text-white text-xs hover:bg-slate-800 shrink-0">
                    {copied === 'json' ? '已复制' : '复制'}
                  </button>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-xs font-medium text-slate-500 w-20 shrink-0">Clash 格式</span>
                  <code className="flex-1 bg-slate-50 px-3 py-2 rounded text-xs font-mono border border-slate-200 truncate">
                    {token.url}?format=clash
                  </code>
                  <button onClick={() => copyURL(token.url + '?format=clash', 'clash')}
                    className="px-3 py-1.5 rounded bg-slate-900 text-white text-xs hover:bg-slate-800 shrink-0">
                    {copied === 'clash' ? '已复制' : '复制'}
                  </button>
                </div>
              </div>
            )}

            <div className="pt-2 border-t border-slate-100">
              <button onClick={regenerate}
                className="px-3 py-1.5 rounded ring-1 ring-rose-300 text-rose-600 text-sm hover:bg-rose-50">
                重新生成 Token
              </button>
              <span className="text-xs text-slate-400 ml-3">重新生成后旧链接立即失效</span>
            </div>

            {/* Published nodes preview */}
            {publishNodes.length > 0 && (
              <div className="pt-4 border-t border-slate-100">
                <h3 className="text-sm font-medium text-slate-700 mb-2">已发布节点 ({publishNodes.length})</h3>
                <div className="space-y-1">
                  {publishNodes.map((node, i) => (
                    <div key={i} className="flex items-center gap-3 px-3 py-2 bg-slate-50 rounded text-sm">
                      <span className="font-medium w-32 truncate">{node.name}</span>
                      <span className="px-1.5 py-0.5 rounded bg-slate-200 text-slate-600 text-xs">{node.mode}</span>
                      <span className="text-slate-500 font-mono text-xs">{node.address}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
            {publishNodes.length === 0 && (
              <div className="pt-4 border-t border-slate-100 text-sm text-slate-400">
                暂无已发布节点（需先在 Dashboard 添加隧道）
              </div>
            )}
          </section>
        )}

        {/* Import Tab */}
        {tab === 'import' && showImport && (
          <section className="bg-white rounded-lg shadow-sm ring-1 ring-slate-200 overflow-hidden">
            <div className="flex items-center justify-between px-4 py-3 border-b border-slate-200">
              <h2 className="font-semibold">订阅源</h2>
              <button onClick={() => setAdding(true)}
                className="px-3 py-1.5 rounded bg-slate-900 text-white text-sm hover:bg-slate-800">+ 添加</button>
            </div>

            {/* Add Form */}
            {adding && (
              <div className="px-4 py-4 border-b border-slate-200 bg-slate-50 space-y-3">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  <input placeholder="名称" value={form.name}
                    onChange={e => setForm({ ...form, name: e.target.value })}
                    className="px-3 py-2 rounded border border-slate-300 text-sm" />
                  <select value={form.format}
                    onChange={e => setForm({ ...form, format: e.target.value })}
                    className="px-3 py-2 rounded border border-slate-300 text-sm">
                    <option value="auto">自动检测</option>
                    <option value="json">SafeLink JSON</option>
                    <option value="clash">Clash YAML</option>
                  </select>
                </div>
                <input placeholder="订阅 URL" value={form.url}
                  onChange={e => setForm({ ...form, url: e.target.value })}
                  className="w-full px-3 py-2 rounded border border-slate-300 text-sm" />
                <div className="flex items-center gap-4">
                  <label className="flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={form.auto_refresh}
                      onChange={e => setForm({ ...form, auto_refresh: e.target.checked })} />
                    自动刷新
                  </label>
                  {form.auto_refresh && (
                    <label className="flex items-center gap-1 text-sm">
                      间隔
                      <input type="number" min={5} value={form.interval_min}
                        onChange={e => setForm({ ...form, interval_min: parseInt(e.target.value) || 60 })}
                        className="w-16 px-2 py-1 rounded border border-slate-300 text-sm" />
                      分钟
                    </label>
                  )}
                </div>
                <div className="flex gap-2">
                  <button onClick={addSource}
                    className="px-3 py-1.5 rounded bg-slate-900 text-white text-sm hover:bg-slate-800">保存</button>
                  <button onClick={() => setAdding(false)}
                    className="px-3 py-1.5 rounded ring-1 ring-slate-300 text-sm hover:bg-slate-50">取消</button>
                </div>
              </div>
            )}

            {/* Import Result */}
            {lastResult && (
              <div className="px-4 py-3 border-b border-slate-200 bg-emerald-50 space-y-2">
                <div className="text-sm text-emerald-700">
                  导入 {lastResult.imported} 个，跳过 {lastResult.skipped} 个
                  {lastResult.errors?.length > 0 && <span className="text-rose-600 ml-2">错误: {lastResult.errors.join('; ')}</span>}
                </div>
                {lastResult.nodes && lastResult.nodes.length > 0 && (
                  <div className="space-y-1">
                    {lastResult.nodes.map((node, i) => (
                      <div key={i} className="flex items-center gap-3 px-3 py-1.5 bg-white rounded text-sm border border-emerald-100">
                        <span className="font-medium w-32 truncate">{node.name}</span>
                        <span className="px-1.5 py-0.5 rounded bg-slate-100 text-slate-600 text-xs">{node.mode}</span>
                        <span className="text-slate-500 font-mono text-xs">{node.address}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}

            {/* Source List */}
            {sources.length === 0 && !adding && (
              <div className="px-4 py-8 text-center text-slate-500 text-sm">暂无订阅源，点击上方"+ 添加"按钮</div>
            )}
            <div className="divide-y divide-slate-100">
              {sources.map(src => (
                <div key={src.id} className="px-4 py-3 flex items-center justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-sm">{src.name}</span>
                      <span className="text-xs px-1.5 py-0.5 rounded bg-slate-100 text-slate-500">{src.format}</span>
                      {src.auto_refresh && (
                        <span className="text-xs px-1.5 py-0.5 rounded bg-blue-50 text-blue-600">自动 {src.interval_min}m</span>
                      )}
                    </div>
                    <div className="text-xs text-slate-400 truncate mt-0.5">{src.url}</div>
                    <div className="text-xs text-slate-400 mt-0.5">
                      上次刷新: {timeAgo(src.last_refresh)} · 隧道数: {src.tunnel_count}
                      {src.last_error && <span className="text-rose-500 ml-2">{src.last_error}</span>}
                    </div>
                  </div>
                  <div className="flex gap-1 shrink-0">
                    <button onClick={() => refreshSource(src.id)}
                      disabled={refreshing === src.id}
                      className="px-2.5 py-1 rounded ring-1 ring-slate-300 text-xs hover:bg-slate-50 disabled:opacity-50">
                      {refreshing === src.id ? '拉取中…' : '刷新'}
                    </button>
                    <button onClick={() => removeSource(src.id)}
                      className="px-2.5 py-1 rounded ring-1 ring-rose-300 text-rose-600 text-xs hover:bg-rose-50">删除</button>
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}
      </main>
    </div>
  )
}
