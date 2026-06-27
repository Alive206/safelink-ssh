import { useEffect, useState } from 'react'
import { auth, ApiError, appRole as roleApi, AppRole } from './api/client'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Logs from './pages/Logs'
import Subscription from './pages/Subscription'

type View = 'loading' | 'login' | 'dashboard' | 'logs' | 'subscription'

// App acts as the top-level router.  We probe /api/tunnels once: if it returns
// 200 we're already logged in (or auth is disabled); 401 means show the login
// page; everything else means the backend is unreachable.
export default function App() {
  const [view, setView] = useState<View>('loading')
  const [role, setRole] = useState<AppRole>('standalone')

  async function probe() {
    try {
      // Fetch role (public endpoint, no auth needed).
      try {
        const r = await roleApi.get()
        setRole(r.role)
      } catch { /* fallback to standalone */ }

      const info = await auth.info()
      if (!info.auth_required) {
        setView('dashboard')
        return
      }
      // Quick 401-test to see if our cookie is still valid.
      try {
        await fetch('/api/tunnels', { credentials: 'include' }).then((r) => {
          if (r.status === 401) setView('login')
          else setView('dashboard')
        })
      } catch {
        setView('login')
      }
    } catch (e) {
      if (e instanceof ApiError) setView('login')
      else setView('login')
    }
  }

  useEffect(() => { probe() }, [])

  async function logout() {
    try { await auth.logout() } catch { /* ignore */ }
    setView('login')
  }

  if (view === 'loading') return <div className="p-6 text-slate-500">加载中…</div>
  if (view === 'login') return <Login onSuccess={() => setView('dashboard')} />
  if (view === 'logs') return <Logs onClose={() => setView('dashboard')} />
  if (view === 'subscription') return <Subscription role={role} onClose={() => setView('dashboard')} />
  return <Dashboard role={role} onLogout={logout} onShowLogs={() => setView('logs')} onShowSubscription={() => setView('subscription')} />
}
