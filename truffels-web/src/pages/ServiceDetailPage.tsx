import { useCallback, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { api, ServiceInstance, UpdateCheck } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'

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
  const [tab, setTab] = useState<'overview' | 'logs' | 'config'>('overview')
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
            {svc.state !== 'running' && (
              <ActionButton label="Start" variant="start" onClick={() => doAction('start')} disabled={actionLoading} />
            )}
            {svc.state !== 'stopped' && (
              <ActionButton label="Stop" variant="stop" onClick={() => doAction('stop')} disabled={actionLoading} />
            )}
            <ActionButton label="Restart" variant="restart" onClick={() => doAction('restart')} disabled={actionLoading} />
          </>
        )}
        {actionMsg && <span className="text-sm text-gray-400 ml-2">{actionMsg}</span>}
      </div>

      {/* Tabs */}
      <div className="flex gap-1 border-b border-border">
        {(svc.template.read_only ? ['overview', 'logs'] as const : ['overview', 'logs', 'config'] as const).map((t) => (
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

      {tab === 'overview' && <OverviewTab svc={svc} updateCheck={updateStatus?.checks?.find(c => c.service_id === id)} />}
      {tab === 'logs' && <LogsTab id={id!} />}
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

function OverviewTab({ svc, updateCheck }: { svc: ServiceInstance; updateCheck?: UpdateCheck | null }) {
  return (
    <div className="space-y-4">
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
              <dd className="text-gray-300 font-mono">{updateCheck.current_version || '—'}</dd>
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
            </>
          )}
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
