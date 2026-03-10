import { useState } from 'react'

interface Props {
  onSetup: () => void
}

export default function SetupPage({ onSetup }: Props) {
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')

    if (password.length < 8) {
      setError('Password must be at least 8 characters')
      return
    }
    if (password !== confirm) {
      setError('Passwords do not match')
      return
    }

    setLoading(true)
    try {
      const res = await fetch('/api/truffels/auth/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      })
      const data = await res.json()
      if (!res.ok) {
        setError(data.error || 'Setup failed')
        return
      }
      onSetup()
    } catch {
      setError('Connection error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center">
      <div className="bg-surface-raised border border-border rounded-lg p-8 w-full max-w-sm">
        <div className="flex items-center gap-2 mb-2 justify-center">
          <img src="/admin/btc.svg" alt="" className="w-8 h-8" />
          <span className="text-xl font-semibold text-accent">Truffels</span>
        </div>
        <p className="text-gray-400 text-sm text-center mb-6">Set your admin password to get started.</p>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Password</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full bg-surface border border-border rounded px-3 py-2 text-gray-200 focus:outline-none focus:border-accent"
              autoFocus
            />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Confirm password</label>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              className="w-full bg-surface border border-border rounded px-3 py-2 text-gray-200 focus:outline-none focus:border-accent"
            />
          </div>
          {error && <p className="text-red-400 text-sm">{error}</p>}
          <button
            type="submit"
            disabled={loading || !password || !confirm}
            className="w-full bg-accent hover:bg-accent/80 text-black font-medium py-2 rounded disabled:opacity-50 transition-colors"
          >
            {loading ? 'Setting up...' : 'Set password'}
          </button>
        </form>
      </div>
    </div>
  )
}
