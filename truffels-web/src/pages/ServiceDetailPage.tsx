import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from 'recharts'
import { api, ServiceInstance, UpdateCheck, UpdateLog, ContainerSnapshot } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'
import {
  type LogSeverity,
  classifyLine,
  severityColor,
  SEVERITY_LEVELS,
  severityAtOrAbove,
} from '@/lib/logUtils'

function formatUptime(startedAt: string): string {
  if (!startedAt) return '-'
  const start = new Date(startedAt).getTime()
  if (isNaN(start)) return '-'
  const secs = Math.floor((Date.now() - start) / 1000)
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ${mins % 60}m`
  const days = Math.floor(hours / 24)
  return `${days}d ${hours % 24}h`
}

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
  const [tab, setTab] = useState<'overview' | 'monitor' | 'logs' | 'config'>('overview')
  const [actionLoading, setActionLoading] = useState(false)
  const [actionMsg, setActionMsg] = useState('')

  const fetcher = useCallback(() => api.service(id!), [id])
  const { data: svc, error, loading, refresh } = useApi(fetcher, 5000)
  const updateFetcher = useCallback(() => api.updateStatus(), [])
  const { data: updateStatus } = useApi(updateFetcher, 30000)

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!svc) return null

  const doAction = async (action: string) => {
    setActionLoading(true)
    setActionMsg('')
    try {
      const result = await api.serviceAction(id!, action)
      if (result.status === 'already_up_to_date') {
        setActionMsg('Already up to date — no restart needed')
      } else {
        setActionMsg(`${action} successful`)
        setTimeout(refresh, 2000)
      }
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
        {!svc.template.read_only && (
          <>
            {svc.state !== 'running' && svc.state !== 'disabled' && (
              <ActionButton label="Start" variant="start" onClick={() => doAction('start')} disabled={actionLoading} />
            )}
            {svc.state !== 'stopped' && svc.state !== 'disabled' && (
              <ActionButton label="Stop" variant="stop" onClick={() => doAction('stop')} disabled={actionLoading} />
            )}
            {svc.state !== 'disabled' && (
              <ActionButton label="Restart" variant="restart" onClick={() => doAction('restart')} disabled={actionLoading} />
            )}
            {svc.enabled ? (
              <button
                onClick={() => doAction('disable')}
                disabled={actionLoading}
                className="px-3 py-1.5 rounded text-sm font-medium text-purple-400 border border-purple-500/30 hover:bg-purple-500/10 transition-colors disabled:opacity-50"
              >
                Disable
              </button>
            ) : (
              <button
                onClick={() => doAction('enable')}
                disabled={actionLoading}
                className="px-3 py-1.5 rounded text-sm font-medium text-green-400 border border-green-500/30 hover:bg-green-500/10 transition-colors disabled:opacity-50"
              >
                Enable
              </button>
            )}
          </>
        )}
        {actionMsg && <span className="text-sm text-gray-400 ml-2">{actionMsg}</span>}
      </div>

      {/* Tabs */}
      <div className="flex gap-1 border-b border-border">
        {(svc.template.read_only ? ['overview', 'monitor', 'logs'] as const : ['overview', 'monitor', 'logs', 'config'] as const).map((t) => (
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

      {tab === 'overview' && <OverviewTab svc={svc} updateCheck={updateStatus?.checks?.find(c => c.service_id === id)} serviceId={id!} />}
      {tab === 'monitor' && <MonitorTab id={id!} />}
      {tab === 'logs' && <LogsTab id={id!} containerNames={svc.template.stack_containers ?? svc.template.container_names} defaultContainer={svc.template.stack_containers && svc.template.container_names.length === 1 ? svc.template.container_names[0] : ''} />}
      {tab === 'config' && <ConfigTab id={id!} />}
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const kb = bytes / 1024
  if (kb < 1024) return `${kb.toFixed(1)} KB`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(1)} MB`
  const gb = mb / 1024
  return `${gb.toFixed(1)} GB`
}

function formatDifficulty(d: number): string {
  if (d >= 1e12) return `${(d / 1e12).toFixed(2)}T`
  if (d >= 1e9) return `${(d / 1e9).toFixed(2)}G`
  if (d >= 1e6) return `${(d / 1e6).toFixed(2)}M`
  return d.toFixed(0)
}

function BitcoinStatsCard() {
  const fetcher = useCallback(() => api.bitcoindStats(), [])
  const { data, error } = useApi(fetcher, 30000)

  if (error) return (
    <Card>
      <CardTitle>Bitcoin Core</CardTitle>
      <p className="text-sm text-red-400">Unable to fetch stats: {error}</p>
    </Card>
  )
  if (!data) return null

  const { blockchain, network, mempool } = data

  return (
    <>
      <Card>
        <CardTitle>Blockchain</CardTitle>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">Block Height</dt>
          <dd className="text-gray-300 font-mono">{blockchain.blocks.toLocaleString()}</dd>
          <dt className="text-gray-500">Sync Progress</dt>
          <dd className="text-gray-300">{(blockchain.verificationprogress * 100).toFixed(4)}%</dd>
          <dt className="text-gray-500">Difficulty</dt>
          <dd className="text-gray-300 font-mono">{formatDifficulty(blockchain.difficulty)}</dd>
          <dt className="text-gray-500">Chain Size</dt>
          <dd className="text-gray-300">{(blockchain.size_on_disk / 1e9).toFixed(1)} GB</dd>
          <dt className="text-gray-500">Best Block</dt>
          <dd className="text-gray-400 font-mono text-xs truncate">{blockchain.bestblockhash}</dd>
          <dt className="text-gray-500">Pruned</dt>
          <dd className="text-gray-300">{blockchain.pruned ? 'Yes' : 'No'}</dd>
        </dl>
      </Card>
      <Card>
        <CardTitle>Network</CardTitle>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">Version</dt>
          <dd className="text-gray-300">{network.subversion.replace(/\//g, '')}</dd>
          <dt className="text-gray-500">Peers</dt>
          <dd className="text-gray-300">{network.connections_in} in / {network.connections_out} out ({network.connections} total)</dd>
        </dl>
      </Card>
      <Card>
        <CardTitle>Mempool</CardTitle>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">Transactions</dt>
          <dd className="text-gray-300 font-mono">{mempool.size.toLocaleString()}</dd>
          <dt className="text-gray-500">Size</dt>
          <dd className="text-gray-300">{formatBytes(mempool.bytes)}</dd>
          <dt className="text-gray-500">Total Fees</dt>
          <dd className="text-gray-300 font-mono">{mempool.total_fee.toFixed(8)} BTC</dd>
          <dt className="text-gray-500">Min Fee Rate</dt>
          <dd className="text-gray-300 font-mono">{(mempool.mempoolminfee * 100_000_000).toFixed(0)} sat/kvB</dd>
        </dl>
      </Card>
    </>
  )
}

function formatLargeNumber(n: number): string {
  if (n >= 1e12) return `${(n / 1e12).toFixed(2)} T`
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)} G`
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)} M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(2)} K`
  return n.toString()
}

function formatHashrate(raw: string): string {
  if (!raw || raw === '0') return '0 H/s'
  // ckpool returns e.g. "1.92M", "655G", "1.5T" — add space and H/s
  const match = raw.match(/^([\d.]+)\s*([KMGTPE]?)$/)
  if (!match) return raw
  return match[2] ? `${match[1]} ${match[2]}H/s` : `${match[1]} H/s`
}

function CkpoolStatsCard() {
  const fetcher = useCallback(() => api.ckpoolStats(), [])
  const { data, error } = useApi(fetcher, 30000)

  if (error) return (
    <Card>
      <CardTitle>Mining Pool</CardTitle>
      <p className="text-sm text-red-400">Unable to fetch stats: {error}</p>
    </Card>
  )
  if (!data) return null

  const { status, hashrates, shares } = data
  const runtimeH = Math.floor(status.runtime / 3600)
  const runtimeD = Math.floor(runtimeH / 24)
  const runtimeStr = runtimeD > 0 ? `${runtimeD}d ${runtimeH % 24}h` : `${runtimeH}h`
  const rejectRate = shares.accepted > 0
    ? ((shares.rejected / (shares.accepted + shares.rejected)) * 100).toFixed(2)
    : '0'

  return (
    <>
      <Card>
        <CardTitle>Hashrate</CardTitle>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">1 min</dt>
          <dd className="text-gray-300 font-mono">{formatHashrate(hashrates.hashrate1m)}</dd>
          <dt className="text-gray-500">5 min</dt>
          <dd className="text-gray-300 font-mono">{formatHashrate(hashrates.hashrate5m)}</dd>
          <dt className="text-gray-500">1 hour</dt>
          <dd className="text-gray-300 font-mono">{formatHashrate(hashrates.hashrate1hr)}</dd>
          <dt className="text-gray-500">1 day</dt>
          <dd className="text-gray-300 font-mono">{formatHashrate(hashrates.hashrate1d)}</dd>
          <dt className="text-gray-500">7 day</dt>
          <dd className="text-gray-300 font-mono">{formatHashrate(hashrates.hashrate7d)}</dd>
        </dl>
      </Card>
      <Card>
        <CardTitle>Pool</CardTitle>
        <dl className="grid grid-cols-2 gap-2 text-sm">
          <dt className="text-gray-500">Workers</dt>
          <dd className="text-gray-300 font-mono">{status.Workers}</dd>
          <dt className="text-gray-500">Users</dt>
          <dd className="text-gray-300 font-mono">{status.Users}</dd>
          <dt className="text-gray-500">Runtime</dt>
          <dd className="text-gray-300">{runtimeStr}</dd>
          <dt className="text-gray-500">Accepted</dt>
          <dd className="text-gray-300 font-mono">{formatLargeNumber(shares.accepted)}</dd>
          <dt className="text-gray-500">Rejected</dt>
          <dd className="text-gray-300 font-mono">{formatLargeNumber(shares.rejected)} ({rejectRate}%)</dd>
          <dt className="text-gray-500">Best Share</dt>
          <dd className="text-gray-300 font-mono">{formatLargeNumber(shares.bestshare)}</dd>
        </dl>
      </Card>
    </>
  )
}

function ElectrsStatsCard() {
  const fetcher = useCallback(() => api.electrsStats(), [])
  const btcFetcher = useCallback(() => api.bitcoindStats(), [])
  const { data, error } = useApi(fetcher, 30000)
  const { data: btcData } = useApi(btcFetcher, 30000)

  if (error) return (
    <Card>
      <CardTitle>Index</CardTitle>
      <p className="text-sm text-red-400">Unable to fetch stats: {error}</p>
    </Card>
  )
  if (!data) return null

  const btcHeight = btcData?.blockchain.blocks
  const synced = btcHeight != null && data.index_height >= btcHeight
  const behind = btcHeight != null ? btcHeight - data.index_height : null

  return (
    <Card>
      <CardTitle>Index</CardTitle>
      <dl className="grid grid-cols-2 gap-2 text-sm">
        <dt className="text-gray-500">Index Height</dt>
        <dd className="text-gray-300 font-mono">{data.index_height.toLocaleString()}</dd>
        {btcHeight != null && (
          <>
            <dt className="text-gray-500">Bitcoin Core</dt>
            <dd className="text-gray-300 font-mono">{btcHeight.toLocaleString()}</dd>
            <dt className="text-gray-500">Status</dt>
            <dd className={synced ? 'text-green-400' : 'text-yellow-400'}>
              {synced ? 'Synced' : `${behind!.toLocaleString()} blocks behind`}
            </dd>
          </>
        )}
      </dl>
    </Card>
  )
}

function OverviewTab({ svc, updateCheck, serviceId }: { svc: ServiceInstance; updateCheck?: UpdateCheck | null; serviceId: string }) {
  const [rollbackConfirm, setRollbackConfirm] = useState(false)
  const [rollbackLoading, setRollbackLoading] = useState(false)
  const [rollbackMsg, setRollbackMsg] = useState('')

  const logsFetcher = useCallback(() => api.updateLogs(serviceId), [serviceId])
  const { data: updateLogs } = useApi(logsFetcher, 30000)

  // Find previous version from last successful update log
  const lastDoneLog = updateLogs?.find((l: UpdateLog) => l.status === 'done')
  const canRollback = !svc.template.floating_tag && !!lastDoneLog?.from_version
    && lastDoneLog.from_version !== updateCheck?.current_version

  const doRollback = async () => {
    setRollbackLoading(true)
    setRollbackMsg('')
    try {
      await api.rollbackService(serviceId)
      setRollbackMsg('Rollback started')
      setRollbackConfirm(false)
    } catch (e: any) {
      setRollbackMsg(`Error: ${e.message}`)
    } finally {
      setRollbackLoading(false)
    }
  }

  return (
    <div className="space-y-4">
      {svc.sync_info?.syncing && (
        <Card>
          <div className="flex items-center justify-between mb-2">
            <CardTitle>Syncing</CardTitle>
            <span className="text-sm text-yellow-400">{svc.sync_info.detail}</span>
          </div>
          <div className="w-full bg-surface rounded-full h-2.5">
            <div
              className="bg-accent h-2.5 rounded-full transition-all duration-500"
              style={{ width: `${Math.min(svc.sync_info.progress * 100, 100)}%` }}
            />
          </div>
        </Card>
      )}
      {svc.template.id === 'bitcoind' && <BitcoinStatsCard />}
      {svc.template.id === 'ckpool' && <CkpoolStatsCard />}
      {svc.template.id === 'electrs' && <ElectrsStatsCard />}
      <Card>
        <CardTitle>Containers</CardTitle>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-gray-500 border-b border-border-subtle">
                <th className="pb-2 pr-4">Name</th>
                <th className="pb-2 pr-4">Status</th>
                <th className="pb-2 pr-4">Health</th>
                <th className="pb-2 pr-4">Uptime</th>
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
                  <td className="py-2 pr-4 text-gray-400">{formatUptime(c.started_at)}</td>
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
          {updateCheck && (
            <>
              <dt className="text-gray-500">Version</dt>
              <dd className="text-gray-300 font-mono">{updateCheck.current_version || '\u2014'}</dd>
              <dt className="text-gray-500">Update</dt>
              <dd>
                {updateCheck.has_update ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-accent/20 text-accent border border-accent/30">
                    {updateCheck.latest_version} available
                  </span>
                ) : (
                  <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-500/20 text-green-400 border border-green-500/30">
                    up to date
                  </span>
                )}
              </dd>
              {canRollback && (
                <>
                  <dt className="text-gray-500">Rollback</dt>
                  <dd>
                    {rollbackConfirm ? (
                      <div className="flex items-center gap-2">
                        <span className="text-sm text-yellow-400">
                          Rollback to {lastDoneLog!.from_version}?
                        </span>
                        <button
                          onClick={doRollback}
                          disabled={rollbackLoading}
                          className="px-2 py-0.5 bg-yellow-600 hover:bg-yellow-700 text-white text-xs font-medium rounded disabled:opacity-50"
                        >
                          {rollbackLoading ? 'Rolling back...' : 'Confirm'}
                        </button>
                        <button
                          onClick={() => setRollbackConfirm(false)}
                          className="px-2 py-0.5 text-xs text-gray-400 hover:text-gray-200"
                        >
                          Cancel
                        </button>
                      </div>
                    ) : (
                      <button
                        onClick={() => { setRollbackConfirm(true); setRollbackMsg('') }}
                        className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-yellow-600/20 text-yellow-400 border border-yellow-600/30 hover:bg-yellow-600/30 transition-colors"
                      >
                        Rollback to {lastDoneLog!.from_version}
                      </button>
                    )}
                    {rollbackMsg && (
                      <p className={`text-xs mt-1 ${rollbackMsg.startsWith('Error') ? 'text-red-400' : 'text-green-400'}`}>
                        {rollbackMsg}
                      </p>
                    )}
                  </dd>
                </>
              )}
            </>
          )}
        </dl>
      </Card>
    </div>
  )
}

type TimeRange = 1 | 6 | 24

const CONTAINER_COLORS = [
  '#f59e0b', '#3b82f6', '#10b981', '#f97316', '#a855f7',
  '#06b6d4', '#ec4899', '#84cc16', '#ef4444', '#6366f1',
]

function formatChartTime(ts: string): string {
  const d = new Date(ts)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function formatChartTimestamp(ts: string): string {
  const d = new Date(ts)
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function formatDataSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const kb = bytes / 1024
  if (kb < 1024) return `${kb.toFixed(1)} KB`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(1)} MB`
  const gb = mb / 1024
  return `${gb.toFixed(2)} GB`
}

interface ChartPoint {
  timestamp: string
  [key: string]: number | string
}

function buildTimeSeries(
  snapshots: ContainerSnapshot[],
  containers: string[],
  valueFn: (s: ContainerSnapshot) => number,
): ChartPoint[] {
  // Group snapshots by timestamp
  const byTime = new Map<string, Map<string, number>>()
  for (const s of snapshots) {
    if (!byTime.has(s.timestamp)) byTime.set(s.timestamp, new Map())
    byTime.get(s.timestamp)!.set(s.container, valueFn(s))
  }

  // Build chart data
  const points: ChartPoint[] = []
  for (const [ts, vals] of byTime) {
    const point: ChartPoint = { timestamp: ts }
    for (const c of containers) {
      point[c] = vals.get(c) ?? 0
    }
    points.push(point)
  }
  return points.sort((a, b) => a.timestamp.localeCompare(b.timestamp))
}

const DUAL_COLORS: Record<string, [string, string]> = {
  network: ['#10b981', '#ef4444'],  // RX green, TX red
  disk: ['#3b82f6', '#ec4899'],     // Read blue, Write pink
}

function buildDualTimeSeries(
  snapshots: ContainerSnapshot[],
  containers: string[],
  valueFnA: (s: ContainerSnapshot) => number,
  valueFnB: (s: ContainerSnapshot) => number,
  labelA: string,
  labelB: string,
): { points: ChartPoint[]; keys: string[] } {
  const byTime = new Map<string, Map<string, { a: number; b: number }>>()
  for (const s of snapshots) {
    if (!byTime.has(s.timestamp)) byTime.set(s.timestamp, new Map())
    byTime.get(s.timestamp)!.set(s.container, { a: valueFnA(s), b: valueFnB(s) })
  }

  const keys: string[] = []
  for (const c of containers) {
    const short = c.replace(/^truffels-/, '')
    if (containers.length === 1) {
      keys.push(labelA, labelB)
    } else {
      keys.push(`${short} ${labelA}`, `${short} ${labelB}`)
    }
  }

  const points: ChartPoint[] = []
  for (const [ts, vals] of byTime) {
    const point: ChartPoint = { timestamp: ts }
    for (const c of containers) {
      const short = c.replace(/^truffels-/, '')
      const v = vals.get(c) ?? { a: 0, b: 0 }
      if (containers.length === 1) {
        point[labelA] = v.a
        point[labelB] = v.b
      } else {
        point[`${short} ${labelA}`] = v.a
        point[`${short} ${labelB}`] = v.b
      }
    }
    points.push(point)
  }
  return { points: points.sort((a, b) => a.timestamp.localeCompare(b.timestamp)), keys }
}

function DualContainerChart({ title, data, keys, colorScheme, domain, formatter }: {
  title: string
  data: ChartPoint[]
  keys: string[]
  colorScheme: 'network' | 'disk'
  domain?: [number | string, number | string]
  formatter?: (value: number) => string
}) {
  if (data.length === 0) {
    return (
      <Card>
        <CardTitle>{title}</CardTitle>
        <div className="h-40 flex items-center justify-center text-gray-500 text-sm">Collecting data...</div>
      </Card>
    )
  }

  const fmt = formatter || ((v: number) => v.toFixed(1))
  const [colorA, colorB] = DUAL_COLORS[colorScheme]

  return (
    <Card>
      <CardTitle>{title}</CardTitle>
      <div className="h-48">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -20 }}>
            <CartesianGrid stroke="rgba(255,255,255,0.05)" strokeDasharray="3 3" />
            <XAxis
              dataKey="timestamp"
              tickFormatter={formatChartTime}
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
              labelFormatter={formatChartTimestamp}
              formatter={(value: number, name: string) => [fmt(value), name]}
            />
            <Legend
              formatter={(value: string) => <span className="text-xs text-gray-400">{value}</span>}
            />
            {keys.map((k, i) => {
              // Alternate between colorA and colorB
              const color = i % 2 === 0 ? colorA : colorB
              return (
                <Area
                  key={k}
                  type="monotone"
                  dataKey={k}
                  stroke={color}
                  fill={color}
                  fillOpacity={0.1}
                  strokeWidth={1.5}
                  dot={false}
                  isAnimationActive={false}
                />
              )
            })}
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </Card>
  )
}

function ContainerChart({ title, data, containers, unit, domain, formatter }: {
  title: string
  data: ChartPoint[]
  containers: string[]
  unit: string
  domain?: [number | string, number | string]
  formatter?: (value: number) => string
}) {
  if (data.length === 0) {
    return (
      <Card>
        <CardTitle>{title}</CardTitle>
        <div className="h-40 flex items-center justify-center text-gray-500 text-sm">Collecting data...</div>
      </Card>
    )
  }

  const fmt = formatter || ((v: number) => `${v.toFixed(1)}${unit}`)
  const shortName = (name: string) => name.replace(/^truffels-/, '')

  return (
    <Card>
      <CardTitle>{title}</CardTitle>
      <div className="h-48">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 4, right: 4, bottom: 0, left: -20 }}>
            <CartesianGrid stroke="rgba(255,255,255,0.05)" strokeDasharray="3 3" />
            <XAxis
              dataKey="timestamp"
              tickFormatter={formatChartTime}
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
              labelFormatter={formatChartTimestamp}
              formatter={(value: number, name: string) => [fmt(value), shortName(name)]}
            />
            {containers.length > 1 && (
              <Legend
                formatter={(value: string) => <span className="text-xs text-gray-400">{shortName(value)}</span>}
              />
            )}
            {containers.map((c, i) => (
              <Area
                key={c}
                type="monotone"
                dataKey={c}
                stroke={CONTAINER_COLORS[i % CONTAINER_COLORS.length]}
                fill={CONTAINER_COLORS[i % CONTAINER_COLORS.length]}
                fillOpacity={0.12}
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
            ))}
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </Card>
  )
}

function MonitorTab({ id }: { id: string }) {
  const [hours, setHours] = useState<TimeRange>(6)
  const fetcher = useCallback(() => api.serviceMonitoring(id, hours), [id, hours])
  const { data, error, loading } = useApi(fetcher, 15000)

  const containers = data?.containers ?? []
  const snapshots = data?.snapshots ?? []
  const current = data?.current ?? []

  const cpuData = useMemo(() => buildTimeSeries(snapshots, containers, s => s.cpu_percent), [snapshots, containers])
  const memData = useMemo(() => buildTimeSeries(snapshots, containers, s => s.mem_usage_mb), [snapshots, containers])
  const netData = useMemo(() => buildDualTimeSeries(
    snapshots, containers,
    s => s.net_rx_bytes / (1024 * 1024),
    s => s.net_tx_bytes / (1024 * 1024),
    'RX', 'TX',
  ), [snapshots, containers])
  const blockData = useMemo(() => buildDualTimeSeries(
    snapshots, containers,
    s => s.block_read_bytes / (1024 * 1024),
    s => s.block_write_bytes / (1024 * 1024),
    'Read', 'Write',
  ), [snapshots, containers])

  if (loading && !data) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
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

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <ContainerChart
          title="CPU Usage (%)"
          data={cpuData}
          containers={containers}
          unit="%"
          domain={[0, 'auto']}
        />
        <ContainerChart
          title="Memory Usage (MB)"
          data={memData}
          containers={containers}
          unit=" MB"
          domain={[0, 'auto']}
          formatter={(v) => `${v.toFixed(0)} MB`}
        />
        <DualContainerChart
          title="Network I/O (per minute)"
          data={netData.points}
          keys={netData.keys}
          colorScheme="network"
          domain={[0, 'auto']}
          formatter={(v) => formatDataSize(v * 1024 * 1024)}
        />
        <DualContainerChart
          title="Block I/O (per minute)"
          data={blockData.points}
          keys={blockData.keys}
          colorScheme="disk"
          domain={[0, 'auto']}
          formatter={(v) => formatDataSize(v * 1024 * 1024)}
        />
      </div>

      {/* Current stats info panel */}
      {current.length > 0 && (
        <Card>
          <CardTitle>Current Totals (since container start)</CardTitle>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-gray-500 border-b border-border-subtle">
                  <th className="pb-2 pr-4">Container</th>
                  <th className="pb-2 pr-4">CPU</th>
                  <th className="pb-2 pr-4">Memory</th>
                  <th className="pb-2 pr-4">Net RX</th>
                  <th className="pb-2 pr-4">Net TX</th>
                  <th className="pb-2 pr-4">Disk Read</th>
                  <th className="pb-2">Disk Write</th>
                </tr>
              </thead>
              <tbody>
                {current.map((c) => (
                  <tr key={c.name} className="border-b border-border-subtle last:border-0">
                    <td className="py-2 pr-4 font-mono text-gray-300 text-xs">{c.name.replace(/^truffels-/, '')}</td>
                    <td className="py-2 pr-4 text-gray-300 font-mono">{c.cpu_percent.toFixed(1)}%</td>
                    <td className="py-2 pr-4 text-gray-300 font-mono">{c.mem_usage_mb.toFixed(0)} / {c.mem_limit_mb.toFixed(0)} MB</td>
                    <td className="py-2 pr-4 text-gray-400 font-mono">{formatDataSize(c.net_rx_bytes)}</td>
                    <td className="py-2 pr-4 text-gray-400 font-mono">{formatDataSize(c.net_tx_bytes)}</td>
                    <td className="py-2 pr-4 text-gray-400 font-mono">{formatDataSize(c.block_read_bytes)}</td>
                    <td className="py-2 text-gray-400 font-mono">{formatDataSize(c.block_write_bytes)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  )
}

const TAIL_OPTIONS = [100, 200, 500, 1000] as const
type SeverityFilter = 'all' | LogSeverity

function LogsTab({ id, containerNames, defaultContainer = '' }: { id: string; containerNames: string[]; defaultContainer?: string }) {
  const [tail, setTail] = useState(200)
  const [filter, setFilter] = useState<SeverityFilter>('all')
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [since, setSince] = useState('')
  const [container, setContainer] = useState(defaultContainer)
  const logEndRef = useRef<HTMLDivElement>(null)

  const fetcher = useCallback(() => api.serviceLogs(id, tail, since, container), [id, tail, since, container])
  const { data, error, loading, refresh } = useApi(fetcher, autoRefresh ? 10000 : 0)

  const handleClear = () => {
    setSince(new Date().toISOString())
    setFilter('all')
  }

  // Classify all lines once
  const classified = useMemo(() => {
    if (!data?.logs) return []
    return data.logs.split('\n').filter((l) => l.trim()).map((line) => ({
      text: line,
      severity: classifyLine(line),
    }))
  }, [data])

  // Count errors/warnings for badges
  const errorCount = useMemo(() => classified.filter((l) => l.severity === 'error').length, [classified])
  const warnCount = useMemo(() => classified.filter((l) => l.severity === 'warn').length, [classified])

  // Cumulative filtered lines — e.g. "warn" shows error + warn, "info" shows error + warn + info
  const filtered = useMemo(() => {
    if (filter === 'all') return classified
    const allowed = severityAtOrAbove(filter)
    return classified.filter((l) => allowed.has(l.severity))
  }, [classified, filter])

  // Auto-scroll to bottom on new data
  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [filtered])

  const filterButtons: { key: SeverityFilter; label: string }[] = [
    { key: 'all', label: 'All' },
    { key: 'error', label: errorCount > 0 ? `Error (${errorCount})` : 'Error' },
    { key: 'warn', label: warnCount > 0 ? `Warn (${warnCount})` : 'Warn' },
    { key: 'info', label: 'Info' },
    { key: 'debug', label: 'Debug' },
  ]

  return (
    <Card>
      <div className="flex flex-col gap-3 mb-3">
        <div className="flex justify-between items-center">
          <CardTitle>Logs{since && <span className="text-xs text-gray-500 font-normal ml-2">(cleared at {new Date(since).toLocaleTimeString()})</span>}</CardTitle>
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-1 text-xs text-gray-400 cursor-pointer">
              <input
                type="checkbox"
                checked={autoRefresh}
                onChange={(e) => setAutoRefresh(e.target.checked)}
                className="rounded bg-surface border-border"
              />
              Auto-refresh
            </label>
            {containerNames.length > 1 && (
              <select
                value={container}
                onChange={(e) => setContainer(e.target.value)}
                className="text-xs bg-surface border border-border rounded px-2 py-1 text-gray-300"
              >
                <option value="">All containers</option>
                {containerNames.map((name) => (
                  <option key={name} value={name}>{name.replace('truffels-', '')}</option>
                ))}
              </select>
            )}
            <select
              value={tail}
              onChange={(e) => setTail(Number(e.target.value))}
              className="text-xs bg-surface border border-border rounded px-2 py-1 text-gray-300"
            >
              {TAIL_OPTIONS.map((n) => (
                <option key={n} value={n}>{n} lines</option>
              ))}
            </select>
            <button onClick={handleClear} className="text-xs text-gray-400 hover:text-gray-200">Clear</button>
            {since && <button onClick={() => setSince('')} className="text-xs text-gray-500 hover:text-gray-300">Show all</button>}
            <button onClick={refresh} className="text-xs text-accent hover:text-accent-hover">Refresh</button>
          </div>
        </div>
        <div className="flex gap-1">
          {filterButtons.map((fb) => (
            <button
              key={fb.key}
              onClick={() => setFilter(fb.key)}
              className={`text-xs px-2 py-1 rounded ${
                filter === fb.key
                  ? 'bg-accent text-white'
                  : fb.key === 'error' && errorCount > 0
                    ? 'bg-surface text-red-400 hover:bg-border'
                    : fb.key === 'warn' && warnCount > 0
                      ? 'bg-surface text-yellow-400 hover:bg-border'
                      : 'bg-surface text-gray-400 hover:bg-border'
              }`}
            >
              {fb.label}
            </button>
          ))}
        </div>
      </div>
      {loading && !data && <div className="text-gray-400">Loading...</div>}
      {error && <div className="text-red-400">{error}</div>}
      {data && (
        <div className="text-xs font-mono bg-surface rounded p-3 overflow-auto max-h-[600px]">
          {filtered.length === 0 ? (
            <span className="text-gray-600">No matching log lines</span>
          ) : (
            filtered.map((line, i) => (
              <div key={i} className={`${severityColor[line.severity]} whitespace-pre-wrap`}>
                {line.text}
              </div>
            ))
          )}
          <div ref={logEndRef} />
        </div>
      )}
    </Card>
  )
}

function ConfigTab({ id }: { id: string }) {
  const fetcher = useCallback(() => api.serviceConfig(id), [id])
  const { data, error, loading, refresh } = useApi(fetcher)
  const [editing, setEditing] = useState(false)
  const [editValue, setEditValue] = useState('')
  const [restartAfter, setRestartAfter] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveMsg, setSaveMsg] = useState('')

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

  const startEdit = () => {
    setEditValue(data.config!)
    setEditing(true)
    setSaveMsg('')
  }

  const cancelEdit = () => {
    setEditing(false)
    setSaveMsg('')
  }

  const saveConfig = async () => {
    setSaving(true)
    setSaveMsg('')
    try {
      await api.updateConfig(id, editValue, restartAfter)
      setSaveMsg(restartAfter ? 'Saved and restarting...' : 'Saved')
      setEditing(false)
      setTimeout(refresh, 1000)
    } catch (e: any) {
      setSaveMsg(`Error: ${e.message}`)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-4">
      <Card>
        <div className="flex justify-between items-center mb-3">
          <CardTitle>Configuration — {data.path}</CardTitle>
          {!editing && (
            <button onClick={startEdit} className="text-xs text-accent hover:text-accent-hover">
              Edit
            </button>
          )}
        </div>
        {editing ? (
          <>
            <textarea
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              className="w-full h-96 text-sm font-mono text-gray-300 bg-surface rounded p-3 border border-border-subtle focus:border-accent focus:outline-none resize-y"
              spellCheck={false}
            />
            <div className="flex items-center gap-4 mt-3">
              <label className="flex items-center gap-2 text-sm text-gray-400">
                <input
                  type="checkbox"
                  checked={restartAfter}
                  onChange={(e) => setRestartAfter(e.target.checked)}
                  className="rounded border-border-subtle"
                />
                Restart service after save
              </label>
              <div className="flex gap-2 ml-auto">
                <button
                  onClick={cancelEdit}
                  disabled={saving}
                  className="px-3 py-1.5 rounded text-sm text-gray-400 hover:text-gray-200 transition-colors"
                >
                  Cancel
                </button>
                <button
                  onClick={saveConfig}
                  disabled={saving}
                  className="px-3 py-1.5 rounded text-sm font-medium text-white bg-accent hover:bg-accent-hover transition-colors disabled:opacity-50"
                >
                  {saving ? 'Saving...' : 'Save'}
                </button>
              </div>
            </div>
          </>
        ) : (
          <pre className="text-sm font-mono text-gray-300 bg-surface rounded p-3 overflow-auto whitespace-pre-wrap">
            {data.config}
          </pre>
        )}
        {saveMsg && (
          <p className={`text-sm mt-2 ${saveMsg.startsWith('Error') ? 'text-red-400' : 'text-green-400'}`}>
            {saveMsg}
          </p>
        )}
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
