import { useCallback } from 'react'
import { Link } from 'react-router-dom'
import { api } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'

function formatUptime(startedAt: string): string {
  if (!startedAt) return ''
  const start = new Date(startedAt).getTime()
  if (isNaN(start)) return ''
  const secs = Math.floor((Date.now() - start) / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ${mins % 60}m`
  const days = Math.floor(hours / 24)
  return `${days}d ${hours % 24}h`
}

function serviceUptime(containers: { started_at: string; status: string }[]): string {
  const running = containers.filter((c) => c.status === 'running')
  if (!running.length) return ''
  const starts = running
    .map((c) => new Date(c.started_at).getTime())
    .filter((t) => !isNaN(t))
  if (!starts.length) return ''
  const oldest = Math.min(...starts)
  const secs = Math.floor((Date.now() - oldest) / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ${mins % 60}m`
  const days = Math.floor(hours / 24)
  return `${days}d ${hours % 24}h`
}

function friendlyIssue(issue: string): string {
  if (issue.includes('fully synced')) return 'needs full sync'
  if (issue.includes('unpruned')) return 'needs unpruned'
  return issue
}

export default function ServicesPage() {
  const fetcher = useCallback(() => api.services(), [])
  const { data, error, loading } = useApi(fetcher, 10000)
  const settingsFetcher = useCallback(() => api.settings(), [])
  const { data: settings } = useApi(settingsFetcher)

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!data) return null

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Services</h1>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {data.map((svc) => (
          <Link key={svc.template.id} to={`/services/${svc.template.id}`}>
            <Card className="hover:border-accent/30 transition-colors h-full">
              <div className="flex items-start justify-between mb-2">
                <h3 className="font-semibold text-gray-100">{svc.template.display_name}</h3>
                <div className="flex items-center gap-2">
                  {svc.sync_info?.syncing && (
                    <span className="px-1.5 py-0.5 rounded text-xs font-medium bg-yellow-500/20 text-yellow-400">
                      Syncing {(svc.sync_info.progress * 100).toFixed(1)}%
                    </span>
                  )}
                  <span className="text-xs text-gray-500">{serviceUptime(svc.containers)}</span>
                  <StatusBadge status={svc.state} />
                </div>
              </div>
              <p className="text-sm text-gray-400 mb-3">{svc.template.description}</p>
              <div className="flex flex-wrap gap-2 text-xs">
                {(settings?.services_show_ports !== false) && svc.template.port && (
                  <span className="px-2 py-0.5 rounded bg-surface-overlay text-gray-400">
                    {svc.template.port}
                  </span>
                )}
                {settings?.services_show_memory && (
                  <span className="px-2 py-0.5 rounded bg-surface-overlay text-gray-400">
                    {svc.template.memory_limit} mem
                  </span>
                )}
                {svc.dependency_issues?.map((issue) => (
                  <span key={issue} className="px-2 py-0.5 rounded bg-yellow-500/20 text-yellow-400">
                    {friendlyIssue(issue)}
                  </span>
                ))}
                {svc.template.dependencies?.map((dep) => (
                  <span key={dep} className="px-2 py-0.5 rounded bg-accent/10 text-accent text-xs">
                    needs {dep}
                  </span>
                ))}
              </div>
              <div className="mt-3 pt-3 border-t border-border-subtle">
                {svc.containers.map((c) => (
                  <div key={c.name} className="flex items-center justify-between text-xs py-0.5">
                    <span className="text-gray-400 font-mono">{c.name}</span>
                    <div className="flex items-center gap-2">
                      <span className="text-gray-500">{c.status === 'running' ? formatUptime(c.started_at) : '-'}</span>
                      <StatusBadge status={c.health || c.status} />
                    </div>
                  </div>
                ))}
              </div>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  )
}
