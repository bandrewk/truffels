import { useCallback } from 'react'
import { Link } from 'react-router-dom'
import { api } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'
import MetricBar from '@/components/MetricBar'

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

export default function DashboardPage() {
  const fetcher = useCallback(() => api.dashboard(), [])
  const { data, error, loading } = useApi(fetcher, 10000)

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!data) return null

  const { host, services, alerts } = data

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Dashboard</h1>

      {/* Host Metrics */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <Card>
          <CardTitle>CPU</CardTitle>
          <MetricBar label="" value={host.cpu_percent} unit="%" />
        </Card>
        <Card>
          <CardTitle>Memory</CardTitle>
          <MetricBar label="" value={host.mem_used_mb} max={host.mem_total_mb} unit="MB" />
        </Card>
        <Card>
          <CardTitle>Temperature</CardTitle>
          <div className="text-3xl font-mono text-gray-100">
            {host.temperature_c.toFixed(1)}<span className="text-lg text-gray-400">°C</span>
          </div>
        </Card>
        <Card>
          <CardTitle>Uptime</CardTitle>
          <div className="text-3xl font-mono text-gray-100">
            {formatUptime(host.uptime_seconds)}
          </div>
        </Card>
      </div>

      {/* Disk */}
      {host.disks.map((disk) => (
        <Card key={disk.path}>
          <CardTitle>Disk — {disk.path}</CardTitle>
          <MetricBar
            label=""
            value={disk.used_gb}
            max={disk.total_gb}
            unit="GB"
            warn={80}
            crit={90}
          />
          <div className="text-xs text-gray-500 mt-1">
            {disk.avail_gb.toFixed(1)} GB free
          </div>
        </Card>
      ))}

      {/* Services */}
      <Card>
        <CardTitle>Services</CardTitle>
        <div className="space-y-2">
          {services.map((svc) => (
            <Link
              key={svc.id}
              to={`/services/${svc.id}`}
              className="flex items-center justify-between p-3 rounded hover:bg-surface-overlay transition-colors"
            >
              <div>
                <span className="font-medium text-gray-200">{svc.display_name}</span>
                <span className="text-xs text-gray-500 ml-2">
                  {svc.containers.length} container{svc.containers.length !== 1 ? 's' : ''}
                </span>
              </div>
              <StatusBadge status={svc.state} />
            </Link>
          ))}
        </div>
      </Card>

      {/* Alerts */}
      {alerts.active_count > 0 && (
        <Card>
          <CardTitle>Active Alerts ({alerts.active_count})</CardTitle>
          <div className="space-y-2">
            {alerts.recent.map((a) => (
              <div key={a.id} className="flex items-center gap-3 p-2 rounded bg-surface-overlay">
                <StatusBadge status={a.severity} />
                <span className="text-sm text-gray-300">{a.message}</span>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  )
}
