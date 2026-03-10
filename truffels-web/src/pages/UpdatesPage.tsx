import { useCallback, useState } from 'react'
import { api, PreflightResult, UpdateCheck, UpdateLog, UpdateSource } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'
import ConfirmDialog from '@/components/ConfirmDialog'

function DockerIcon() {
  return (
    <svg viewBox="0 0 24 24" className="w-4 h-4" fill="currentColor">
      <path d="M13.98 11.08h2.12a.19.19 0 0 0 .19-.19V9.01a.19.19 0 0 0-.19-.19h-2.12a.19.19 0 0 0-.19.19v1.88c0 .1.09.19.19.19m-2.95-5.43h2.12a.19.19 0 0 0 .19-.19V3.57a.19.19 0 0 0-.19-.19h-2.12a.19.19 0 0 0-.19.19v1.89c0 .1.09.19.19.19m0 2.71h2.12a.19.19 0 0 0 .19-.19V6.29a.19.19 0 0 0-.19-.19h-2.12a.19.19 0 0 0-.19.19v1.88c0 .1.09.19.19.19m-2.93 0h2.12a.19.19 0 0 0 .19-.19V6.29a.19.19 0 0 0-.19-.19H8.1a.19.19 0 0 0-.19.19v1.88c0 .1.08.19.19.19m-2.96 0h2.12a.19.19 0 0 0 .19-.19V6.29a.19.19 0 0 0-.19-.19H5.14a.19.19 0 0 0-.19.19v1.88c0 .1.09.19.19.19m5.89 2.72h2.12a.19.19 0 0 0 .19-.19V9.01a.19.19 0 0 0-.19-.19h-2.12a.19.19 0 0 0-.19.19v1.88c0 .1.09.19.19.19m-2.93 0h2.12a.19.19 0 0 0 .19-.19V9.01a.19.19 0 0 0-.19-.19H8.1a.19.19 0 0 0-.19.19v1.88c0 .1.08.19.19.19m-2.96 0h2.12a.19.19 0 0 0 .19-.19V9.01a.19.19 0 0 0-.19-.19H5.14a.19.19 0 0 0-.19.19v1.88c0 .1.09.19.19.19m-2.92 0h2.12a.19.19 0 0 0 .19-.19V9.01a.19.19 0 0 0-.19-.19H2.22a.19.19 0 0 0-.19.19v1.88c0 .1.08.19.19.19M24 11.76a4.3 4.3 0 0 0-2.16-1.46c.02-.2.01-.4-.01-.6-.26-1.7-1.68-2.88-2.95-2.88-.4 0-.76.1-1.07.31l-.1.07c-.1.07-.2.16-.28.25a5 5 0 0 0-.52.73c-.22-.08-.46-.14-.7-.17a2 2 0 0 0-.27-.02H13.6V3.57A.19.19 0 0 0 13.41 3.38h-2.12a.19.19 0 0 0-.19.19V5.46H2.22a.19.19 0 0 0-.19.19v1.88c-.01.1.08.19.19.19h.6v3.08c0 .1.08.19.19.19h.56c.1 0 .19-.09.19-.19V8.82h13.15c.3.03.57.13.8.28l-.03.08a4.7 4.7 0 0 0-.18 1.37c0 1.05.37 2.04 1.04 2.78.68.77 1.62 1.2 2.62 1.2h.05c.56 0 1.12-.17 1.57-.44.49-.31.87-.71 1.12-1.15a2.7 2.7 0 0 0 .34-.87c.04-.2.05-.38.04-.56a1.9 1.9 0 0 0-.28-.75"/>
    </svg>
  )
}

function GitHubIcon() {
  return (
    <svg viewBox="0 0 24 24" className="w-4 h-4" fill="currentColor">
      <path d="M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.17 6.839 9.49.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.34-3.369-1.34-.454-1.156-1.11-1.464-1.11-1.464-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.578 9.578 0 0 1 12 6.836c.85.004 1.705.114 2.504.336 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.202 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.167 22 16.418 22 12c0-5.523-4.477-10-10-10z"/>
    </svg>
  )
}

function BitbucketIcon() {
  return (
    <svg viewBox="0 0 24 24" className="w-4 h-4" fill="currentColor">
      <path d="M2.65 3C2.29 3 2 3.29 2 3.65c0 .04 0 .08.01.13l2.74 16.6c.1.54.57.93 1.12.93h12.41c.41 0 .77-.29.83-.7L21.99 3.78c.04-.36-.22-.68-.57-.72-.04 0-.08-.01-.12-.01H2.65zM14.1 14.95H9.94L8.78 9.05h6.3l-1 5.9z"/>
    </svg>
  )
}

function SourceLinks({ source }: { source?: UpdateSource }) {
  if (!source) return null

  if (source.type === 'dockerhub') {
    const images = source.images || []
    return (
      <div className="flex items-center gap-1.5 flex-wrap">
        {images.map((img, i) => (
          <a
            key={img}
            href={`https://hub.docker.com/r/${img}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-300 transition-colors"
            title={img}
          >
            <span>{img}</span>
            {i === images.length - 1 && <DockerIcon />}
          </a>
        ))}
      </div>
    )
  }

  let url: string
  let icon: React.ReactNode
  let label: string

  switch (source.type) {
    case 'github':
      url = `https://github.com/${source.repo || ''}`
      icon = <GitHubIcon />
      label = source.repo || 'GitHub'
      break
    case 'bitbucket':
      url = `https://bitbucket.org/${source.repo || ''}`
      icon = <BitbucketIcon />
      label = source.repo || 'Bitbucket'
      break
    default:
      return null
  }

  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-center gap-1 text-xs text-gray-500 hover:text-gray-300 transition-colors"
      title={label}
    >
      <span>{label}</span>
      {icon}
    </a>
  )
}

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
  const [preflightResult, setPreflightResult] = useState<PreflightResult | null>(null)
  const [preflightLoading, setPreflightLoading] = useState<string | null>(null)

  async function handleCheck() {
    setActionPending('check')
    try {
      await api.checkUpdates()
      setTimeout(() => { refreshStatus(); refreshLogs() }, 3000)
    } finally {
      setActionPending(null)
    }
  }

  async function handlePreflight(serviceId: string) {
    setPreflightLoading(serviceId)
    try {
      const result = await api.updatePreflight(serviceId)
      setPreflightResult(result)
    } catch {
      setPreflightResult(null)
    } finally {
      setPreflightLoading(null)
    }
  }

  async function handleConfirmUpdate() {
    if (!preflightResult) return
    const serviceId = preflightResult.service_id
    setPreflightResult(null)
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
  const sources = status?.sources || {}

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
                    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-500/20 text-green-400 border border-green-500/30">
                      up to date
                    </span>
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
              </div>
              <div className="flex-shrink-0">
                {c.has_update && !c.error && !updating[c.service_id] && (
                  <button
                    onClick={() => handlePreflight(c.service_id)}
                    disabled={actionPending !== null || preflightLoading !== null}
                    className="px-3 py-1.5 text-sm rounded bg-accent/20 hover:bg-accent/30 text-accent transition-colors disabled:opacity-50"
                  >
                    {preflightLoading === c.service_id ? 'Checking...' : actionPending === c.service_id ? 'Updating...' : 'Update'}
                  </button>
                )}
              </div>
            </div>
            <div className="flex items-center justify-between mt-2 flex-wrap gap-2">
              <p className="text-xs text-gray-600">Checked {formatTime(c.checked_at)}</p>
              <SourceLinks source={sources[c.service_id]} />
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

      {/* Preflight Confirmation Dialog */}
      <ConfirmDialog
        open={preflightResult !== null}
        title={`Update ${preflightResult?.service_id}?`}
        onConfirm={handleConfirmUpdate}
        onCancel={() => setPreflightResult(null)}
        confirmLabel="Confirm Update"
        confirmDisabled={!preflightResult?.can_proceed}
      >
        {preflightResult && (
          <div className="space-y-4">
            <div className="text-sm text-gray-400">
              <span className="font-mono text-gray-200">{preflightResult.from_version}</span>
              <span className="text-gray-600 mx-2">&rarr;</span>
              <span className="font-mono text-gray-200">{preflightResult.to_version}</span>
            </div>

            <div className="space-y-2">
              {preflightResult.checks.map((check, i) => (
                <div key={i} className="flex items-start gap-2 text-sm">
                  <span className={`flex-shrink-0 mt-0.5 ${
                    check.status === 'pass' ? 'text-green-400' :
                    check.status === 'fail' ? 'text-red-400' :
                    'text-yellow-400'
                  }`}>
                    {check.status === 'pass' ? '\u2713' : check.status === 'fail' ? '\u2717' : '\u26A0'}
                  </span>
                  <div className="min-w-0">
                    <span className="text-gray-300">{check.name}</span>
                    <p className="text-xs text-gray-500">{check.message}</p>
                  </div>
                </div>
              ))}
            </div>

            {!preflightResult.can_proceed && (
              <p className="text-sm text-red-400">Cannot proceed — resolve issues above.</p>
            )}
          </div>
        )}
      </ConfirmDialog>

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
