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
  const [menuOpen, setMenuOpen] = useState(false)

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
      <header className="bg-surface-raised border-b border-border px-4 sm:px-6 py-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <img src="/admin/btc.svg" alt="" className="w-7 h-7" />
            <span className="text-lg font-semibold text-accent">Truffels</span>
          </div>

          {/* Desktop nav */}
          <nav className="hidden sm:flex items-center gap-1 ml-6 flex-1">
            {navItems.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.to === '/'}
                className={({ isActive }) =>
                  `px-3 py-1.5 rounded text-sm font-medium transition-colors whitespace-nowrap ${
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
          <div className="hidden sm:block ml-auto">
            <button
              onClick={handleLogout}
              className="text-sm text-gray-400 hover:text-gray-200 transition-colors whitespace-nowrap"
            >
              Sign out
            </button>
          </div>

          {/* Mobile hamburger */}
          <button
            onClick={() => setMenuOpen(!menuOpen)}
            className="sm:hidden p-1.5 text-gray-400 hover:text-gray-200"
            aria-label="Toggle menu"
          >
            <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              {menuOpen ? (
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              ) : (
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
              )}
            </svg>
          </button>
        </div>

        {/* Mobile menu */}
        {menuOpen && (
          <nav className="sm:hidden mt-3 pt-3 border-t border-border flex flex-col gap-1">
            {navItems.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.to === '/'}
                onClick={() => setMenuOpen(false)}
                className={({ isActive }) =>
                  `px-3 py-2 rounded text-sm font-medium transition-colors ${
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
            <button
              onClick={() => { setMenuOpen(false); handleLogout() }}
              className="px-3 py-2 text-left text-sm text-gray-400 hover:text-gray-200 transition-colors"
            >
              Sign out
            </button>
          </nav>
        )}
      </header>
      <main className="flex-1 p-4 sm:p-6 max-w-7xl mx-auto w-full">
        <Outlet />
      </main>
    </div>
  )
}
