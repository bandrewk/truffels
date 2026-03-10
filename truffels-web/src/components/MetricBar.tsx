export default function MetricBar({ label, value, max, unit, warn = 75, crit = 90 }: {
  label: string
  value: number
  max?: number
  unit: string
  warn?: number
  crit?: number
}) {
  const pct = max ? (value / max) * 100 : value
  const color = pct >= crit ? 'bg-red-500' : pct >= warn ? 'bg-yellow-500' : 'bg-green-500'

  return (
    <div>
      <div className="flex justify-between text-sm mb-1">
        <span className="text-gray-400">{label}</span>
        <span className="text-gray-200 font-mono">
          {max ? `${value.toFixed(0)} / ${max.toFixed(0)} ${unit}` : `${value.toFixed(1)}${unit}`}
        </span>
      </div>
      <div className="h-2 bg-surface-overlay rounded-full overflow-hidden">
        <div className={`h-full rounded-full transition-all ${color}`} style={{ width: `${Math.min(pct, 100)}%` }} />
      </div>
    </div>
  )
}
