import { useCallback, useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'

const navItems = [
  { to: '/', label: 'Dashboard' },
  { to: '/services', label: 'Services' },
  { to: '/alerts', label: 'Alerts' },
  { to: '/updates', label: 'Updates' },
]

interface Props {
  onLogout: () => void
}

export default function Layout({ onLogout }: Props) {
  const [pendingUpdates, setPendingUpdates] = useState(0)

  const fetchUpdateCount = useCallback(async () => {
    try {
      const res = await fetch('/api/truffels/updates/status')
      if (res.ok) {
        const data = await res.json()
        setPendingUpdates(data.pending_count || 0)
      }
    } catch {
      // ignore
    }
  }, [])

  useEffect(() => {
    fetchUpdateCount()
    const id = setInterval(fetchUpdateCount, 60000)
    return () => clearInterval(id)
  }, [fetchUpdateCount])

  async function handleLogout() {
    await fetch('/api/truffels/auth/logout', { method: 'POST' })
    onLogout()
  }

  return (
    <div className="min-h-screen flex flex-col">
      <header className="bg-surface-raised border-b border-border px-6 py-3 flex items-center gap-6">
        <div className="flex items-center gap-2">
          <img src="/admin/btc.svg" alt="" className="w-7 h-7" />
          <span className="text-lg font-semibold text-accent">Truffels</span>
        </div>
        <nav className="flex gap-1 ml-6">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/'}
              className={({ isActive }) =>
                `px-3 py-1.5 rounded text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-accent/10 text-accent'
                    : 'text-gray-400 hover:text-gray-200 hover:bg-surface-overlay'
                }`
              }
            >
              {item.label}
              {item.to === '/updates' && pendingUpdates > 0 && (
                <span className="ml-1.5 inline-flex items-center justify-center w-5 h-5 text-[10px] font-bold rounded-full bg-accent text-black">
                  {pendingUpdates}
                </span>
              )}
            </NavLink>
          ))}
        </nav>
        <div className="ml-auto">
          <button
            onClick={handleLogout}
            className="text-sm text-gray-400 hover:text-gray-200 transition-colors"
          >
            Sign out
          </button>
        </div>
      </header>
      <main className="flex-1 p-6 max-w-7xl mx-auto w-full">
        <Outlet />
      </main>
    </div>
  )
}
