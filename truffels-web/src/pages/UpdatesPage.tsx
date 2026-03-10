import { useCallback, useState } from 'react'
import { api, UpdateCheck, UpdateLog } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'

function formatTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString('de-DE', { day: '2-digit', month: '2-digit', year: '2-digit', hour: '2-digit', minute: '2-digit' })
}

export default function UpdatesPage() {
  const statusFetcher = useCallback(() => api.updateStatus(), [])
  const logsFetcher = useCallback(() => api.updateLogs(), [])
  const { data: status, loading, refresh: refreshStatus } = useApi(statusFetcher, 10000)
  const { data: logs, refresh: refreshLogs } = useApi(logsFetcher, 10000)
  const [actionPending, setActionPending] = useState<string | null>(null)

  async function handleCheck() {
    setActionPending('check')
    try {
      await api.checkUpdates()
      setTimeout(() => { refreshStatus(); refreshLogs() }, 3000)
    } finally {
      setActionPending(null)
    }
  }

  async function handleApply(serviceId: string) {
    setActionPending(serviceId)
    try {
      await api.applyUpdate(serviceId)
      setTimeout(() => { refreshStatus(); refreshLogs() }, 5000)
    } finally {
      setActionPending(null)
    }
  }

  async function handleApplyAll() {
    setActionPending('all')
    try {
      await api.applyAllUpdates()
      setTimeout(() => { refreshStatus(); refreshLogs() }, 5000)
    } finally {
      setActionPending(null)
    }
  }

  if (loading) return <div className="text-gray-400">Loading...</div>

  const checks = status?.checks || []
  const updating = status?.updating || {}
  const pendingCount = status?.pending_count || 0

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <h1 className="text-2xl font-bold">Updates</h1>
        <div className="flex gap-2">
          <button
            onClick={handleCheck}
            disabled={actionPending !== null}
            className="px-4 py-2 text-sm rounded bg-surface-overlay hover:bg-surface-raised text-gray-200 transition-colors disabled:opacity-50"
          >
            {actionPending === 'check' ? 'Checking...' : 'Check Now'}
          </button>
          {pendingCount > 0 && (
            <button
              onClick={handleApplyAll}
              disabled={actionPending !== null}
              className="px-4 py-2 text-sm rounded bg-accent/20 hover:bg-accent/30 text-accent transition-colors disabled:opacity-50"
            >
              {actionPending === 'all' ? 'Updating...' : `Update All (${pendingCount})`}
            </button>
          )}
        </div>
      </div>

      {/* Service Update Cards */}
      <div className="space-y-3">
        {checks.map((c: UpdateCheck) => (
          <Card key={c.service_id}>
            <div className="flex items-start justify-between gap-3 flex-wrap">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-medium text-gray-200">{c.service_id}</span>
                  {c.error ? (
                    <span className="text-xs text-red-400">error</span>
                  ) : updating[c.service_id] ? (
                    <span className="text-xs text-yellow-400 animate-pulse">updating...</span>
                  ) : c.has_update ? (
                    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-accent/20 text-accent border border-accent/30">
                      update available
                    </span>
                  ) : (
                    <span className="text-xs text-gray-500">up to date</span>
                  )}
                </div>
                <div className="flex items-center gap-4 mt-1.5 text-sm flex-wrap">
                  <span className="text-gray-400">
                    <span className="text-gray-500">Current: </span>
                    <span className="font-mono">{c.current_version || '—'}</span>
                  </span>
                  {c.latest_version && c.latest_version !== c.current_version && (
                    <span className="text-gray-400">
                      <span className="text-gray-500">Latest: </span>
                      <span className="font-mono">{c.latest_version}</span>
                    </span>
                  )}
                </div>
                {c.error && (
                  <p className="text-xs text-red-400 mt-1">{c.error}</p>
                )}
                <p className="text-xs text-gray-600 mt-1">Checked {formatTime(c.checked_at)}</p>
              </div>
              <div className="flex-shrink-0">
                {c.has_update && !c.error && !updating[c.service_id] && (
                  <button
                    onClick={() => handleApply(c.service_id)}
                    disabled={actionPending !== null}
                    className="px-3 py-1.5 text-sm rounded bg-accent/20 hover:bg-accent/30 text-accent transition-colors disabled:opacity-50"
                  >
                    {actionPending === c.service_id ? 'Updating...' : 'Update'}
                  </button>
                )}
              </div>
            </div>
          </Card>
        ))}
        {checks.length === 0 && (
          <Card>
            <p className="text-sm text-gray-500 text-center py-4">
              No update checks yet. Click "Check Now" to scan for updates.
            </p>
          </Card>
        )}
      </div>

      {/* Update History */}
      {logs && logs.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold mb-3">Update History</h2>
          <div className="space-y-2">
            {logs.map((l: UpdateLog) => (
              <Card key={l.id}>
                <div className="flex items-start justify-between gap-3 flex-wrap">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-medium text-gray-200">{l.service_id}</span>
                      <StatusBadge status={logStatusMap(l.status)} />
                    </div>
                    <div className="flex items-center gap-2 mt-1 text-xs text-gray-400 flex-wrap">
                      <span className="font-mono">{l.from_version}</span>
                      <span className="text-gray-600">&rarr;</span>
                      <span className="font-mono">{l.to_version}</span>
                    </div>
                    {l.error && (
                      <p className="text-xs text-red-400 mt-1">{l.error}</p>
                    )}
                  </div>
                  <span className="text-xs text-gray-500 flex-shrink-0">{formatTime(l.started_at)}</span>
                </div>
              </Card>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function logStatusMap(status: string): string {
  switch (status) {
    case 'done': return 'running'
    case 'failed': return 'critical'
    case 'rolled_back': return 'warning'
    case 'pulling':
    case 'building':
    case 'restarting': return 'degraded'
    default: return 'unknown'
  }
}
