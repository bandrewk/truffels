import { NavLink, Outlet } from 'react-router-dom'

const navItems = [
  { to: '/', label: 'Dashboard' },
  { to: '/services', label: 'Services' },
  { to: '/alerts', label: 'Alerts' },
]

export default function Layout() {
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
            </NavLink>
          ))}
        </nav>
      </header>
      <main className="flex-1 p-6 max-w-7xl mx-auto w-full">
        <Outlet />
      </main>
    </div>
  )
}
