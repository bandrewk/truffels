/** Truncate a Docker digest for display. */
export function truncDigest(v: string): string {
  if (!v) return '—'
  if (v.startsWith('sha256:')) return v.slice(0, 19) + '…'
  return v
}

/** Format an ISO timestamp for display. */
export function formatTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  return d.toLocaleString('de-DE', { day: '2-digit', month: '2-digit', year: '2-digit', hour: '2-digit', minute: '2-digit' })
}

/** Map update log status to StatusBadge status string. */
export function logStatusMap(status: string): string {
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

/** Human label for an in-progress update log status. */
export function phaseLabel(status: string): string {
  switch (status) {
    case 'pending': return 'Queued'
    case 'pulling': return 'Pulling'
    case 'building': return 'Building'
    case 'restarting': return 'Restarting'
    default: return 'Updating'
  }
}

/** Format elapsed time since an ISO timestamp as "47s", "2m 14s", or "1h 03m". */
export function formatElapsed(iso: string): string {
  if (!iso) return ''
  const startMs = Date.parse(iso)
  if (isNaN(startMs)) return ''
  const secs = Math.max(0, Math.floor((Date.now() - startMs) / 1000))
  if (secs < 60) return `${secs}s`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ${String(secs % 60).padStart(2, '0')}s`
  const hours = Math.floor(mins / 60)
  return `${hours}h ${String(mins % 60).padStart(2, '0')}m`
}
