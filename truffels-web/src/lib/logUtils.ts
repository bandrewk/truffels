export type LogSeverity = 'error' | 'warn' | 'info' | 'debug' | 'unknown'

export function classifyLine(line: string): LogSeverity {
  // Try JSON log format first (mempool-backend, caddy)
  if (line.startsWith('{')) {
    try {
      const obj = JSON.parse(line)
      const lvl = (obj.level || obj.lvl || '').toLowerCase()
      if (lvl === 'error' || lvl === 'fatal' || lvl === 'panic') return 'error'
      if (lvl === 'warn' || lvl === 'warning') return 'warn'
      if (lvl === 'info' || lvl === 'notice') return 'info'
      if (lvl === 'debug' || lvl === 'trace') return 'debug'
    } catch { /* not JSON, fall through */ }
  }
  const upper = line.toUpperCase()
  if (/\b(ERROR|FATAL|PANIC|CRITICAL)\b/.test(upper)) return 'error'
  if (/\b(WARN|WARNING)\b/.test(upper)) return 'warn'
  if (/\b(INFO|NOTICE)\b/.test(upper)) return 'info'
  if (/\b(DEBUG|TRACE)\b/.test(upper)) return 'debug'
  return 'unknown'
}

export const severityColor: Record<LogSeverity, string> = {
  error: 'text-red-400',
  warn: 'text-yellow-400',
  info: 'text-gray-400',
  debug: 'text-gray-600',
  unknown: 'text-gray-400',
}

export const SEVERITY_LEVELS: LogSeverity[] = ['error', 'warn', 'info', 'debug']

export function severityAtOrAbove(threshold: LogSeverity): Set<LogSeverity> {
  const idx = SEVERITY_LEVELS.indexOf(threshold)
  return new Set(SEVERITY_LEVELS.slice(0, idx + 1))
}
