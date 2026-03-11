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
