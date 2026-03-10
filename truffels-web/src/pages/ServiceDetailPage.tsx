import { useCallback, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, ServiceInstance } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'

function ActionButton({ label, variant, onClick, disabled }: {
  label: string; variant: 'start' | 'stop' | 'restart'; onClick: () => void; disabled: boolean
}) {
  const colors = {
    start: 'bg-green-600 hover:bg-green-700',
    stop: 'bg-red-600 hover:bg-red-700',
    restart: 'bg-yellow-600 hover:bg-yellow-700',
  }
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`px-3 py-1.5 rounded text-sm font-medium text-white transition-colors disabled:opacity-50 ${colors[variant]}`}
    >
      {label}
    </button>
  )
}

export default function ServiceDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [tab, setTab] = useState<'overview' | 'logs' | 'config'>('overview')
  const [actionLoading, setActionLoading] = useState(false)
  const [actionMsg, setActionMsg] = useState('')

  const fetcher = useCallback(() => api.service(id!), [id])
  const { data: svc, error, loading, refresh } = useApi(fetcher, 5000)

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!svc) return null

  const doAction = async (action: string) => {
    setActionLoading(true)
    setActionMsg('')
    try {
      await api.serviceAction(id!, action)
      setActionMsg(`${action} successful`)
      setTimeout(refresh, 2000)
    } catch (e: any) {
      setActionMsg(`Error: ${e.message}`)
    } finally {
      setActionLoading(false)
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <Link to="/services" className="text-gray-400 hover:text-gray-200">&larr;</Link>
        <h1 className="text-2xl font-bold">{svc.template.display_name}</h1>
        <StatusBadge status={svc.state} />
      </div>

      <p className="text-gray-400">{svc.template.description}</p>

      {/* Actions */}
      <div className="flex gap-2 items-center">
        <ActionButton label="Start" variant="start" onClick={() => doAction('start')} disabled={actionLoading} />
        <ActionButton label="Stop" variant="stop" onClick={() => doAction('stop')} disabled={actionLoading} />
        <ActionButton label="Restart" variant="restart" onClick={() => doAction('restart')} disabled={actionLoading} />
        {actionMsg && <span className="text-sm text-gray-400 ml-2">{actionMsg}</span>}
      </div>

      {/* Tabs */}
      <div className="flex gap-1 border-b border-border">
        {(['overview', 'logs', 'config'] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
              tab === t ? 'border-accent text-accent' : 'border-transparent text-gray-400 hover:text-gray-200'
            }`}
          >
            {t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === 'overview' && <OverviewTab svc={svc} />}
      {tab === 'logs' && <LogsTab id={id!} />}
      {tab === 'config' && <ConfigTab id={id!} />}
    </div>
  )
}

function OverviewTab({ svc }: { svc: ServiceInstance }) {
  return (
    <div className="space-y-4">
      <Card>
        <CardTitle>Containers</CardTitle>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-gray-500 border-b border-border-subtle">
                <th className="pb-2 pr-4">Name</th>
                <th className="pb-2 pr-4">Status</th>
                <th className="pb-2 pr-4">Health</th>
                <th className="pb-2 pr-4">Restarts</th>
                <th className="pb-2">Image</th>
              </tr>
            </thead>
            <tbody>
              {svc.containers.map((c) => (
                <tr key={c.name} className="border-b border-border-subtle last:border-0">
                  <td className="py-2 pr-4 font-mono text-gray-300">{c.name}</td>
                  <td className="py-2 pr-4"><StatusBadge status={c.status} /></td>
                  <td className="py-2 pr-4">{c.health ? <StatusBadge status={c.health} /> : <span className="text-gray-500">-</span>}</td>
                  <td className="py-2 pr-4 text-gray-400">{c.restart_count}</td>
                  <td className="py-2 text-gray-500 text-xs font-mono truncate max-w-xs">{c.image.split('@')[0]}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      <Card>
        <CardTitle>Info</CardTitle>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">Memory Limit</dt>
          <dd className="text-gray-300">{svc.template.memory_limit}</dd>
          {svc.template.port && (
            <>
              <dt className="text-gray-500">Port</dt>
              <dd className="text-gray-300">{svc.template.port}</dd>
            </>
          )}
          <dt className="text-gray-500">Dependencies</dt>
          <dd className="text-gray-300">{svc.template.dependencies?.join(', ') || 'None'}</dd>
        </dl>
      </Card>
    </div>
  )
}

function LogsTab({ id }: { id: string }) {
  const fetcher = useCallback(() => api.serviceLogs(id, 200), [id])
  const { data, error, loading, refresh } = useApi(fetcher)

  return (
    <Card>
      <div className="flex justify-between items-center mb-3">
        <CardTitle>Logs (last 200 lines)</CardTitle>
        <button onClick={refresh} className="text-xs text-accent hover:text-accent-hover">Refresh</button>
      </div>
      {loading && <div className="text-gray-400">Loading...</div>}
      {error && <div className="text-red-400">{error}</div>}
      {data && (
        <pre className="text-xs font-mono text-gray-400 bg-surface rounded p-3 overflow-auto max-h-[600px] whitespace-pre-wrap">
          {data.logs || 'No logs available'}
        </pre>
      )}
    </Card>
  )
}

function ConfigTab({ id }: { id: string }) {
  const fetcher = useCallback(() => api.serviceConfig(id), [id])
  const { data, error, loading } = useApi(fetcher)

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">{error}</div>
  if (!data) return null

  if (!data.config) {
    return (
      <Card>
        <p className="text-gray-400 text-sm">{data.message || 'No config file for this service.'}</p>
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardTitle>Configuration — {data.path}</CardTitle>
        <pre className="text-sm font-mono text-gray-300 bg-surface rounded p-3 overflow-auto whitespace-pre-wrap">
          {data.config}
        </pre>
      </Card>

      {data.revisions.length > 0 && (
        <Card>
          <CardTitle>Revision History</CardTitle>
          <div className="space-y-2">
            {data.revisions.map((rev) => (
              <div key={rev.id} className="text-xs text-gray-400 p-2 rounded bg-surface-overlay">
                <span className="text-gray-500">{rev.timestamp}</span>
                <span className="mx-2">|</span>
                <span>{rev.actor}</span>
                <span className="mx-2">|</span>
                <span>{rev.diff}</span>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  )
}
