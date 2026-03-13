const stateColors: Record<string, string> = {
  running: 'bg-green-500/20 text-green-400 border-green-500/30',
  healthy: 'bg-green-500/20 text-green-400 border-green-500/30',
  stopped: 'bg-gray-500/20 text-gray-400 border-gray-500/30',
  degraded: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30',
  unhealthy: 'bg-red-500/20 text-red-400 border-red-500/30',
  unknown: 'bg-gray-500/20 text-gray-400 border-gray-500/30',
  disabled: 'bg-purple-500/20 text-purple-400 border-purple-500/30',
  exited: 'bg-red-500/20 text-red-400 border-red-500/30',
  warning: 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30',
  critical: 'bg-red-500/20 text-red-400 border-red-500/30',
}

export default function StatusBadge({ status }: { status: string }) {
  const colors = stateColors[status] || stateColors.unknown
  return (
    <span className={`inline-flex items-center justify-center w-20 px-2 py-0.5 rounded text-xs font-medium border ${colors}`}>
      {status}
    </span>
  )
}
