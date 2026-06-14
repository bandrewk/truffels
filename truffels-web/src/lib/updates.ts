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

/**
 * Parse a version string into a sortable []number.
 * Strips leading "v" and any non-numeric suffix (e.g. "31.0-arm64" → [31, 0],
 * "16.14-alpine" → [16, 14]). Tag variants that share a semver compare equal
 * — they're the same release, differentiated by the tag string, not the
 * version math (the action button in UpdatesPage handles that case).
 */
export function parseVersion(v: string): number[] {
  let s = v.replace(/^v/, '')
  s = s.replace(/[^0-9.].*$/, '')
  if (!s) return []
  return s.split('.').map((p) => parseInt(p, 10)).filter((n) => !isNaN(n))
}

/**
 * Compare two version strings. Returns negative if a < b, positive if a > b,
 * 0 if they parse to the same []number (which includes tag-variant pairs
 * like "31.0" vs "31.0-arm64").
 */
export function compareVersion(a: string, b: string): number {
  const pa = parseVersion(a), pb = parseVersion(b)
  const n = Math.max(pa.length, pb.length)
  for (let i = 0; i < n; i++) {
    const av = pa[i] ?? 0, bv = pb[i] ?? 0
    if (av !== bv) return av - bv
  }
  return 0
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
