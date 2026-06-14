import { useEffect, useState } from 'react'

// Full-screen overlay shown while the truffels stack is restarting during
// a self-update. Mirrors RebootOverlay: poll /health, redirect to /admin/
// on recovery. After 10 min of being stuck, surface a manual escape hatch.
export function UpdatingOverlay() {
  const [elapsed, setElapsed] = useState(0)
  const [status, setStatus] = useState<'updating' | 'polling' | 'online'>('updating')

  useEffect(() => {
    const timer = setInterval(() => setElapsed((s) => s + 1), 1000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    if (elapsed < 15) return
    if (status === 'online') return
    if (status === 'updating') setStatus('polling')

    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 3000)
    fetch('/api/truffels/health', { signal: controller.signal })
      .then((r) => { if (r.ok) setStatus('online') })
      .catch(() => {})
      .finally(() => clearTimeout(timeout))
  }, [elapsed, status])

  useEffect(() => {
    if (status === 'online') {
      const t = setTimeout(() => { window.location.href = '/admin/' }, 1500)
      return () => clearTimeout(t)
    }
  }, [status])

  const minutes = Math.floor(elapsed / 60)
  const seconds = elapsed % 60
  const timeStr = `${minutes}:${seconds.toString().padStart(2, '0')}`
  const stuck = elapsed >= 10 * 60 && status !== 'online'

  return (
    <div className="fixed inset-0 z-50 bg-black/95 flex items-center justify-center">
      <div className="text-center space-y-6 max-w-md px-4">
        <div className={`text-6xl ${status === 'online' ? 'text-green-500' : stuck ? 'text-red-500' : 'text-yellow-500'}`}>
          {status === 'online' ? '✓' : stuck ? '⚠' : '⏳'}
        </div>
        <h2 className="text-2xl font-bold text-white">
          {status === 'online'
            ? 'Truffels Updated'
            : stuck
              ? 'Update is taking longer than expected'
              : 'Truffels is updating'}
        </h2>
        <p className="text-gray-400">
          {status === 'online'
            ? 'Redirecting...'
            : stuck
              ? 'The truffels stack should normally come back within a few minutes. If you have host access, check `docker logs truffels-api` for clues.'
              : status === 'polling'
                ? 'Waiting for the API to come back online…'
                : 'The truffels stack is being recreated. Auto-refresh will resume when it returns.'}
        </p>
        <div className="text-4xl font-mono text-gray-300">{timeStr}</div>
        {status === 'polling' && !stuck && (
          <div className="flex justify-center">
            <div className="w-8 h-8 border-2 border-yellow-500 border-t-transparent rounded-full animate-spin" />
          </div>
        )}
        {stuck && (
          <button
            onClick={() => window.location.reload()}
            className="px-4 py-2 bg-red-600 hover:bg-red-700 text-white text-sm font-medium rounded"
          >
            Reload page
          </button>
        )}
      </div>
    </div>
  )
}
