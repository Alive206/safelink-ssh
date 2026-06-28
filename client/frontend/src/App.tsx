import { useState, useEffect } from 'react'

function App() {
  const [tunnels, setTunnels] = useState<any[]>([])
  const [version, setVersion] = useState('')

  useEffect(() => {
    // @ts-ignore - Wails bindings
    if (window.go?.main?.App) {
      window.go.main.App.GetVersion().then(setVersion)
      window.go.main.App.ListTunnels().then(setTunnels)
    }
  }, [])

  return (
    <div className="min-h-screen bg-gray-900 text-white p-6">
      <header className="mb-8">
        <h1 className="text-2xl font-bold">SafeLink</h1>
        <p className="text-gray-400 text-sm">v{version}</p>
      </header>

      <main>
        <section className="mb-6">
          <h2 className="text-lg font-semibold mb-3">Tunnels</h2>
          {tunnels.length === 0 ? (
            <p className="text-gray-500">No tunnels configured. Add one to get started.</p>
          ) : (
            <div className="space-y-2">
              {tunnels.map((t: any) => (
                <div key={t.config.name} className="bg-gray-800 rounded-lg p-4 flex items-center justify-between">
                  <div>
                    <span className="font-medium">{t.config.name}</span>
                    <span className="ml-2 text-xs text-gray-400">{t.config.mode}</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className={`text-xs px-2 py-1 rounded ${
                      t.state === 'running' ? 'bg-green-700' :
                      t.state === 'connecting' ? 'bg-yellow-700' :
                      'bg-gray-700'
                    }`}>
                      {t.state}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </main>
    </div>
  )
}

export default App
