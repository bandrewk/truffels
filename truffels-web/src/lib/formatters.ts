export function formatUptime(startedAt: string): string {
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

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const kb = bytes / 1024
  if (kb < 1024) return `${kb.toFixed(1)} KB`
  const mb = kb / 1024
  if (mb < 1024) return `${mb.toFixed(1)} MB`
  const gb = mb / 1024
  return `${gb.toFixed(1)} GB`
}

export function formatDifficulty(d: number): string {
  if (d >= 1e12) return `${(d / 1e12).toFixed(2)}T`
  if (d >= 1e9) return `${(d / 1e9).toFixed(2)}G`
  if (d >= 1e6) return `${(d / 1e6).toFixed(2)}M`
  return d.toFixed(0)
}

export function formatLargeNumber(n: number): string {
  if (n >= 1e12) return `${(n / 1e12).toFixed(2)} T`
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)} G`
  if (n >= 1e6) return `${(n / 1e6).toFixed(2)} M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(2)} K`
  return n.toString()
}

export function formatHashrate(raw: string): string {
  if (!raw || raw === '0') return '0 H/s'
  // ckpool returns e.g. "1.92M", "655G", "1.5T" — add space and H/s
  const match = raw.match(/^([\d.]+)\s*([KMGTPE]?)$/)
  if (!match) return raw
  return match[2] ? `${match[1]} ${match[2]}H/s` : `${match[1]} H/s`
}
