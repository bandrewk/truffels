import { useCallback, useState } from 'react'
import { api } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'

const ALERT_TYPE_LABELS: Record<string, string> = {
  disk_full: 'Disk Full',
  high_temp: 'High Temperature',
  service_unhealthy: 'Service Unhealthy',
  restart_loop: 'Restart Loop',
  upstream_unhealthy: 'Upstream Unhealthy',
  memory_trend: 'Memory Trend',
  disk_trend: 'Disk Trend',
  temp_trend: 'Temperature Trend',
}

export default function AlertsPage() {
  const [showAll, setShowAll] = useState(false)
  const fetcher = useCallback(() => api.alerts(showAll), [showAll])
  const { data, error, loading, refresh } = useApi(fetcher, 10000)

  const dismissAlert = async (id: number) => {
    try {
      await api.dismissAlert(id)
      refresh()
    } catch (e) {
      // ignore — will show on next refresh
    }
  }

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>

  const alerts = data || []

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Alerts</h1>
        <label className="flex items-center gap-2 text-sm text-gray-400 cursor-pointer">
          <input
            type="checkbox"
            checked={showAll}
            onChange={(e) => setShowAll(e.target.checked)}
            className="rounded bg-surface-overlay border-border"
          />
          Show resolved
        </label>
      </div>

      {alerts.length === 0 ? (
        <Card>
          <p className="text-gray-400 text-center py-8">
            {showAll ? 'No alerts recorded.' : 'No active alerts. All clear.'}
          </p>
        </Card>
      ) : (
        <Card>
          <CardTitle>{showAll ? 'All Alerts' : 'Active Alerts'}</CardTitle>
          <div className="space-y-2">
            {alerts.map((a) => (
              <div
                key={a.id}
                className={`flex items-start gap-3 p-3 rounded ${
                  a.resolved ? 'bg-surface opacity-60' : 'bg-surface-overlay'
                }`}
              >
                <StatusBadge status={a.severity} />
                <div className="flex-1 min-w-0">
                  <p className="text-sm text-gray-200">{a.message}</p>
                  <div className="flex gap-3 text-xs text-gray-500 mt-1">
                    <span>Type: {ALERT_TYPE_LABELS[a.type] || a.type}</span>
                    {a.service_id && <span>Service: {a.service_id}</span>}
                    <span>Since: {new Date(a.first_seen).toLocaleString()}</span>
                    {a.resolved && a.resolved_at && (
                      <span className="text-green-500">
                        Resolved: {new Date(a.resolved_at).toLocaleString()}
                      </span>
                    )}
                  </div>
                </div>
                {!a.resolved && (
                  <button
                    onClick={() => dismissAlert(a.id)}
                    className="text-gray-500 hover:text-gray-300 p-1 rounded hover:bg-surface transition-colors"
                    title="Dismiss alert"
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
                      <path fillRule="evenodd" d="M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z" clipRule="evenodd" />
                    </svg>
                  </button>
                )}
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  )
}
