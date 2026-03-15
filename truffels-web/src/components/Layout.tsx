import { useCallback, useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'

const navItems = [
  { to: '/', label: 'Dashboard' },
  { to: '/services', label: 'Services' },
  { to: '/alerts', label: 'Alerts' },
  { to: '/updates', label: 'Updates' },
  { to: '/monitoring', label: 'Monitoring' },
  { to: '/settings', label: 'Settings' },
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
      <footer className="border-t border-border px-4 sm:px-6 py-3 flex items-center justify-center gap-3 text-xs text-gray-500">
        <span>Truffels {import.meta.env.VITE_APP_VERSION || 'dev'}</span>
        <span>·</span>
        <a
          href="https://github.com/bandrewk/truffels"
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-gray-500 hover:text-gray-300 transition-colors"
        >
          <svg className="w-3.5 h-3.5" viewBox="0 0 16 16" fill="currentColor">
            <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
          </svg>
          GitHub
        </a>
        <span>·</span>
        <a
          href="bitcoin:bc1q5sl35at30wtftl4je7p0pwwxhwtekfe23602tj"
          className="inline-flex items-center gap-1 text-gray-500 hover:text-gray-300 transition-colors"
          title="Donate BTC"
        >
          <svg className="w-3.5 h-3.5" viewBox="0 0 16 16" fill="currentColor">
            <path d="M10.73 6.77c-.24-1.6-1.6-2.07-3.1-2.22V2.5H6.24v1.97c-.36 0-.74.01-1.11.02V2.5H3.74v2.05H1.5v1.4h1.06c.42 0 .56.22.6.44v6.72c-.02.15-.12.38-.44.38H1.66L1.5 15h2.24v2.05h1.39V15c.38.01.75.01 1.11.01v2.04h1.39v-2.07c2.32-.14 3.94-.74 4.14-2.99.17-1.81-.68-2.62-2.03-2.94.82-.39 1.33-1.14 1.19-2.38zM9.84 12c0 1.83-3.14 1.62-4.14 1.62v-3.24c1 0 4.14-.28 4.14 1.62zm-.69-4.56c0 1.67-2.62 1.47-3.45 1.47V5.74c.83 0 3.45-.26 3.45 1.7z" />
          </svg>
          Donate
        </a>
      </footer>
    </div>
  )
}
