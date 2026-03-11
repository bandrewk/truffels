import { useState, useEffect } from 'react'
import { Routes, Route } from 'react-router-dom'
import Layout from './components/Layout'
import DashboardPage from './pages/DashboardPage'
import ServicesPage from './pages/ServicesPage'
import ServiceDetailPage from './pages/ServiceDetailPage'
import AlertsPage from './pages/AlertsPage'
import UpdatesPage from './pages/UpdatesPage'
import MonitoringPage from './pages/MonitoringPage'
import LoginPage from './pages/LoginPage'
import SetupPage from './pages/SetupPage'

type AuthState = 'loading' | 'setup' | 'login' | 'authenticated'

export default function App() {
  const [authState, setAuthState] = useState<AuthState>('loading')

  useEffect(() => {
    checkAuth()
  }, [])

  async function checkAuth() {
    try {
      const res = await fetch('/api/truffels/auth/status')
      const data = await res.json()
      if (!data.setup) {
        setAuthState('setup')
      } else if (!data.authenticated) {
        setAuthState('login')
      } else {
        setAuthState('authenticated')
      }
    } catch {
      setAuthState('login')
    }
  }

  if (authState === 'loading') {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="text-gray-400">Loading...</div>
      </div>
    )
  }

  if (authState === 'setup') {
    return <SetupPage onSetup={() => setAuthState('authenticated')} />
  }

  if (authState === 'login') {
    return <LoginPage onLogin={() => setAuthState('authenticated')} />
  }

  return (
    <Routes>
      <Route element={<Layout onLogout={() => setAuthState('login')} />}>
        <Route index element={<DashboardPage />} />
        <Route path="services" element={<ServicesPage />} />
        <Route path="services/:id" element={<ServiceDetailPage />} />
        <Route path="alerts" element={<AlertsPage />} />
        <Route path="updates" element={<UpdatesPage />} />
        <Route path="monitoring" element={<MonitoringPage />} />
      </Route>
    </Routes>
  )
}
