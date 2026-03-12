import { useCallback, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import { api, MetricSnapshot, MonitoringResponse } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'

type TimeRange = 1 | 6 | 24

const CHART_COLORS = {
  cpu: '#f59e0b',
  mem: '#3b82f6',
  temp: '#f97316',
  fan: '#06b6d4',
  disk: '#a855f7',
  netRx: '#10b981',
  netTx: '#ef4444',
  diskIO: '#ec4899',
} as const

function formatDataSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function formatTime(ts: string): string {
  const d = new Date(ts)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function formatTimestamp(ts: string): string {
  const d = new Date(ts)
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function formatUptime(startedAt: string): string {
  const secs = Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000)
  if (secs < 0 || isNaN(secs)) return ''
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ${mins % 60}m`
  const days = Math.floor(hours / 24)
  return `${days}d ${hours % 24}h`
}

interface ChartProps {
  data: MetricSnapshot[]
  dataKey: keyof MetricSnapshot
  color: string
  label: string
  unit: string
  current: number
  avg: number
  peak: number
  domain?: [number, number]
}

function MetricChart({ data, dataKey, color, label, unit, current, avg, peak, domain }: ChartProps) {
  if (data.length === 0) {
    return (
      <Card>
        <CardTitle>{label}</CardTitle>
        <div className="h-40 flex items-center justify-center text-gray-500 text-sm">
          Collecting data...
        </div>
      </Card>
    )
  }

  return (
    <Card>
      <CardTitle>{label}</CardTitle>
      <div className="h-40">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -20 }}>
            <CartesianGrid stroke="rgba(255,255,255,0.05)" strokeDasharray="3 3" />
            <XAxis
              dataKey="timestamp"
              tickFormatter={formatTime}
              tick={{ fill: '#6b7280', fontSize: 11 }}
              axisLine={false}
              tickLine={false}
            />
            <YAxis
              domain={domain || ['auto', 'auto']}
              tick={{ fill: '#6b7280', fontSize: 11 }}
              axisLine={false}
              tickLine={false}
            />
            <Tooltip
              contentStyle={{ background: '#1e1e2e', border: '1px solid #2e2e3e', borderRadius: 8, fontSize: 12 }}
              labelFormatter={formatTimestamp}
              formatter={(value: number) => [`${value.toFixed(1)}${unit}`, label]}
            />
            <Area
              type="monotone"
              dataKey={dataKey}
              stroke={color}
              fill={color}
              fillOpacity={0.15}
              strokeWidth={1.5}
              dot={false}
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div className="flex gap-4 mt-2 text-xs text-gray-400">
        <span>Current: <span className="text-gray-200 font-mono">{current.toFixed(1)}{unit}</span></span>
        <span>Avg: <span className="text-gray-200 font-mono">{avg.toFixed(1)}{unit}</span></span>
        <span>Peak: <span className="text-gray-200 font-mono">{peak.toFixed(1)}{unit}</span></span>
      </div>
    </Card>
  )
}

type SortKey = 'service' | 'container' | 'status' | 'health' | 'uptime' | 'restarts'

export default function MonitoringPage() {
  const [hours, setHours] = useState<TimeRange>(24)
  const [sortKey, setSortKey] = useState<SortKey>('service')
  const [sortAsc, setSortAsc] = useState(true)
  const [eventFilter, setEventFilter] = useState<string>('all')

  const fetcher = useCallback(() => api.monitoring(hours), [hours])
  const { data, error, loading } = useApi(fetcher, 10000)

  const sortedContainers = useMemo(() => {
    if (!data) return []
    const sorted = [...data.containers]
    sorted.sort((a, b) => {
      let cmp = 0
      switch (sortKey) {
        case 'service': cmp = a.display_name.localeCompare(b.display_name); break
        case 'container': cmp = a.name.localeCompare(b.name); break
        case 'status': cmp = a.status.localeCompare(b.status); break
        case 'health': cmp = a.health.localeCompare(b.health); break
        case 'uptime': cmp = new Date(a.started_at).getTime() - new Date(b.started_at).getTime(); break
        case 'restarts': cmp = a.restart_count - b.restart_count; break
      }
      return sortAsc ? cmp : -cmp
    })
    return sorted
  }, [data, sortKey, sortAsc])

  const filteredEvents = useMemo(() => {
    if (!data) return []
    if (eventFilter === 'all') return data.events
    return data.events.filter((e) => e.service_id === eventFilter)
  }, [data, eventFilter])

  const serviceIds = useMemo(() => {
    if (!data) return []
    return [...new Set(data.events.map((e) => e.service_id))]
  }, [data])

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!data) return null

  const { metrics, alerts } = data

  function handleSort(key: SortKey) {
    if (sortKey === key) setSortAsc(!sortAsc)
    else { setSortKey(key); setSortAsc(true) }
  }

  function SortHeader({ k, children }: { k: SortKey; children: React.ReactNode }) {
    return (
      <th
        className="px-3 py-2 text-left text-xs font-medium text-gray-400 cursor-pointer select-none hover:text-gray-200 transition-colors"
        onClick={() => handleSort(k)}
      >
        {children}
        {sortKey === k && <span className="ml-1">{sortAsc ? '\u25b2' : '\u25bc'}</span>}
      </th>
    )
  }

  const diskCurrent = metrics.current.disks?.[0]?.used_percent ?? 0

  // Compute disk avg/peak/min from history
  let diskAvg = 0, diskMax = 0
  if (metrics.history.length > 0) {
    let sum = 0
    for (const s of metrics.history) {
      sum += s.disk_percent
      if (s.disk_percent > diskMax) diskMax = s.disk_percent
    }
    diskAvg = sum / metrics.history.length
  }

  const eventDotColor: Record<string, string> = {
    state_change: 'bg-red-400',
    health_change: 'bg-red-400',
    restart: 'bg-yellow-400',
  }

  function eventMessage(e: MonitoringResponse['events'][0]): string {
    if (e.message) return e.message
    if (e.event_type === 'restart') return `${e.container} restarted`
    return `${e.container}: ${e.from_state} \u2192 ${e.to_state}`
  }

  function isRecovery(e: MonitoringResponse['events'][0]): boolean {
    return (e.to_state === 'running' || e.to_state === 'healthy') &&
           (e.from_state !== 'running' && e.from_state !== 'healthy')
  }

  const activeAlerts = alerts.filter((a) => !a.resolved)

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <h1 className="text-2xl font-bold">Monitoring</h1>
        <div className="flex gap-1 bg-surface-overlay rounded-lg p-0.5">
          {([1, 6, 24] as TimeRange[]).map((h) => (
            <button
              key={h}
              onClick={() => setHours(h)}
              className={`px-3 py-1 rounded text-sm font-medium transition-colors ${
                hours === h
                  ? 'bg-accent text-black'
                  : 'text-gray-400 hover:text-gray-200'
              }`}
            >
              {h}h
            </button>
          ))}
        </div>
      </div>

      {/* Section A: Resource Trends */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <MetricChart
          data={metrics.history}
          dataKey="cpu_percent"
          color={CHART_COLORS.cpu}
          label="CPU Usage (%)"
          unit="%"
          current={metrics.current.cpu_percent}
          avg={metrics.summary.cpu_avg}
          peak={metrics.summary.cpu_max}
          domain={[0, 100]}
        />
        <MetricChart
          data={metrics.history}
          dataKey="mem_percent"
          color={CHART_COLORS.mem}
          label="Memory Usage (%)"
          unit="%"
          current={metrics.current.mem_percent}
          avg={metrics.summary.mem_avg}
          peak={metrics.summary.mem_max}
          domain={[0, 100]}
        />
        <Card>
          <CardTitle>Temperature / Fan</CardTitle>
          {metrics.history.length === 0 ? (
            <div className="h-40 flex items-center justify-center text-gray-500 text-sm">Collecting data...</div>
          ) : (
            <>
              <div className="h-40">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={metrics.history} margin={{ top: 4, right: 4, bottom: 0, left: -20 }}>
                    <CartesianGrid stroke="rgba(255,255,255,0.05)" strokeDasharray="3 3" />
                    <XAxis
                      dataKey="timestamp"
                      tickFormatter={formatTime}
                      tick={{ fill: '#6b7280', fontSize: 11 }}
                      axisLine={false}
                      tickLine={false}
                    />
                    <YAxis
                      domain={[0, 100]}
                      tick={{ fill: '#6b7280', fontSize: 11 }}
                      axisLine={false}
                      tickLine={false}
                    />
                    <Tooltip
                      contentStyle={{ background: '#1e1e2e', border: '1px solid #2e2e3e', borderRadius: 8, fontSize: 12 }}
                      labelFormatter={formatTimestamp}
                      formatter={(value: number, name: string) => {
                        if (name === 'temp_c') return [`${value.toFixed(1)}°C`, 'Temp']
                        if (name === 'fan_percent') return [`${value}%`, 'Fan']
                        return [value, name]
                      }}
                    />
                    <Area
                      type="monotone"
                      dataKey="temp_c"
                      stroke={CHART_COLORS.temp}
                      fill={CHART_COLORS.temp}
                      fillOpacity={0.15}
                      strokeWidth={1.5}
                      dot={false}
                      isAnimationActive={false}
                    />
                    <Area
                      type="monotone"
                      dataKey="fan_percent"
                      stroke={CHART_COLORS.fan}
                      fill={CHART_COLORS.fan}
                      fillOpacity={0.08}
                      strokeWidth={1.5}
                      dot={false}
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
              <div className="flex gap-4 mt-2 text-xs text-gray-400 flex-wrap">
                <span style={{ color: CHART_COLORS.temp }}>Temp:</span>
                <span>Current: <span className="text-gray-200 font-mono">{metrics.current.temperature_c.toFixed(1)}°C</span></span>
                <span>Avg: <span className="text-gray-200 font-mono">{metrics.summary.temp_avg.toFixed(1)}°C</span></span>
                <span>Peak: <span className="text-gray-200 font-mono">{metrics.summary.temp_max.toFixed(1)}°C</span></span>
                <span className="ml-2" style={{ color: CHART_COLORS.fan }}>Fan:</span>
                <span>Current: <span className="text-gray-200 font-mono">{metrics.current.fan_percent}%</span></span>
              </div>
            </>
          )}
        </Card>
        <MetricChart
          data={metrics.history}
          dataKey="disk_percent"
          color={CHART_COLORS.disk}
          label="Disk Usage (%)"
          unit="%"
          current={diskCurrent}
          avg={diskAvg}
          peak={diskMax}
          domain={[0, 100]}
        />
        <Card>
          <CardTitle>Network I/O (per minute)</CardTitle>
          {metrics.history.length === 0 ? (
            <div className="h-40 flex items-center justify-center text-gray-500 text-sm">Collecting data...</div>
          ) : (
            <>
              <div className="h-40">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={metrics.history} margin={{ top: 4, right: 4, bottom: 0, left: -20 }}>
                    <CartesianGrid stroke="rgba(255,255,255,0.05)" strokeDasharray="3 3" />
                    <XAxis
                      dataKey="timestamp"
                      tickFormatter={formatTime}
                      tick={{ fill: '#6b7280', fontSize: 11 }}
                      axisLine={false}
                      tickLine={false}
                    />
                    <YAxis
                      tick={{ fill: '#6b7280', fontSize: 11 }}
                      axisLine={false}
                      tickLine={false}
                      tickFormatter={(v: number) => formatDataSize(v)}
                    />
                    <Tooltip
                      contentStyle={{ background: '#1e1e2e', border: '1px solid #2e2e3e', borderRadius: 8, fontSize: 12 }}
                      labelFormatter={formatTimestamp}
                      formatter={(value: number, name: string) => {
                        const label = name === 'net_rx_bytes' ? 'RX' : 'TX'
                        return [formatDataSize(value), label]
                      }}
                    />
                    <Area
                      type="monotone"
                      dataKey="net_rx_bytes"
                      stroke={CHART_COLORS.netRx}
                      fill={CHART_COLORS.netRx}
                      fillOpacity={0.15}
                      strokeWidth={1.5}
                      dot={false}
                      isAnimationActive={false}
                    />
                    <Area
                      type="monotone"
                      dataKey="net_tx_bytes"
                      stroke={CHART_COLORS.netTx}
                      fill={CHART_COLORS.netTx}
                      fillOpacity={0.1}
                      strokeWidth={1.5}
                      dot={false}
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
              <div className="flex gap-4 mt-2 text-xs text-gray-400 flex-wrap">
                <span style={{ color: CHART_COLORS.netRx }}>RX:</span>
                <span>Current: <span className="text-gray-200 font-mono">{formatDataSize(metrics.current.net_rx_bytes ?? 0)}/min</span></span>
                <span className="ml-2" style={{ color: CHART_COLORS.netTx }}>TX:</span>
                <span>Current: <span className="text-gray-200 font-mono">{formatDataSize(metrics.current.net_tx_bytes ?? 0)}/min</span></span>
              </div>
            </>
          )}
        </Card>
        <MetricChart
          data={metrics.history}
          dataKey="disk_io_percent"
          color={CHART_COLORS.diskIO}
          label="Disk I/O Utilization (%)"
          unit="%"
          current={metrics.history.length > 0 ? metrics.history[metrics.history.length - 1].disk_io_percent : 0}
          avg={metrics.history.length > 0 ? metrics.history.reduce((s, h) => s + h.disk_io_percent, 0) / metrics.history.length : 0}
          peak={metrics.history.length > 0 ? Math.max(...metrics.history.map(h => h.disk_io_percent)) : 0}
          domain={[0, 100]}
        />
      </div>

      {/* Section B: Container Status Table */}
      <Card>
        <CardTitle>Container Status</CardTitle>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border">
                <SortHeader k="service">Service</SortHeader>
                <SortHeader k="container">Container</SortHeader>
                <SortHeader k="status">Status</SortHeader>
                <SortHeader k="health">Health</SortHeader>
                <SortHeader k="uptime">Uptime</SortHeader>
                <SortHeader k="restarts">Restarts</SortHeader>
                <th className="px-3 py-2 text-left text-xs font-medium text-gray-400">Image</th>
              </tr>
            </thead>
            <tbody>
              {sortedContainers.map((c) => (
                <tr key={c.name} className="border-b border-border/50 hover:bg-surface-overlay transition-colors">
                  <td className="px-3 py-2 text-gray-200">{c.display_name}</td>
                  <td className="px-3 py-2 text-gray-400 font-mono text-xs">{c.name}</td>
                  <td className="px-3 py-2"><StatusBadge status={c.status} /></td>
                  <td className="px-3 py-2"><StatusBadge status={c.health || 'unknown'} /></td>
                  <td className="px-3 py-2 text-gray-400 font-mono text-xs">{formatUptime(c.started_at)}</td>
                  <td className="px-3 py-2">
                    <span className={`font-mono text-xs ${
                      c.restart_count > 3 ? 'text-red-400' : c.restart_count > 0 ? 'text-yellow-400' : 'text-gray-400'
                    }`}>
                      {c.restart_count}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-gray-500 text-xs truncate max-w-[200px]">{c.image}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      {/* Section C: Health Timeline */}
      <Card>
        <div className="flex items-center justify-between mb-3">
          <CardTitle>Health Timeline</CardTitle>
          <select
            value={eventFilter}
            onChange={(e) => setEventFilter(e.target.value)}
            className="bg-surface-overlay border border-border rounded px-2 py-1 text-xs text-gray-300 focus:outline-none focus:border-accent"
          >
            <option value="all">All services</option>
            {serviceIds.map((id) => (
              <option key={id} value={id}>{id}</option>
            ))}
          </select>
        </div>
        {filteredEvents.length === 0 ? (
          <div className="text-sm text-gray-500 py-4 text-center">No events in this period</div>
        ) : (
          <div className="space-y-1 max-h-80 overflow-y-auto">
            {filteredEvents.map((e) => {
              const recovery = isRecovery(e)
              const dotColor = recovery ? 'bg-green-400' : (eventDotColor[e.event_type] || 'bg-gray-400')
              return (
                <div key={e.id} className="flex items-start gap-3 py-1.5 px-2 rounded hover:bg-surface-overlay transition-colors">
                  <span className="text-xs text-gray-500 font-mono whitespace-nowrap mt-0.5">
                    {formatTimestamp(e.timestamp)}
                  </span>
                  <span className={`w-2 h-2 rounded-full mt-1.5 flex-shrink-0 ${dotColor}`} />
                  <span className="text-sm text-gray-300">{eventMessage(e)}</span>
                </div>
              )
            })}
          </div>
        )}
      </Card>

      {/* Section D: Actionable Errors */}
      <Card>
        <CardTitle>Actionable Errors</CardTitle>
        {activeAlerts.length === 0 ? (
          <div className="flex items-center gap-2 py-4">
            <span className="w-2 h-2 rounded-full bg-green-400" />
            <span className="text-sm text-green-400">All Clear</span>
          </div>
        ) : (
          <div className="space-y-2">
            {activeAlerts.map((a) => (
              <div key={a.id} className="flex items-center justify-between p-3 rounded bg-surface-overlay">
                <div className="flex items-center gap-3">
                  <StatusBadge status={a.severity} />
                  <div>
                    <div className="text-sm text-gray-200">{a.message}</div>
                    <div className="text-xs text-gray-500">Since {formatTimestamp(a.first_seen)}</div>
                  </div>
                </div>
                <div className="flex gap-2">
                  <Link
                    to={`/services/${a.service_id}`}
                    className="text-xs px-2 py-1 rounded bg-surface-raised border border-border text-gray-300 hover:text-gray-100 transition-colors"
                  >
                    View Logs
                  </Link>
                  {(a.type === 'service_unhealthy' || a.type === 'restart_loop') && (
                    <button
                      onClick={() => api.serviceAction(a.service_id, 'restart')}
                      className="text-xs px-2 py-1 rounded bg-accent/10 text-accent border border-accent/20 hover:bg-accent/20 transition-colors"
                    >
                      Restart
                    </button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}
