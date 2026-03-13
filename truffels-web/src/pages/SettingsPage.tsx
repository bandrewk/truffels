import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, Settings } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'
import {
  type LogSeverity,
  classifyLine,
  severityColor,
  SEVERITY_LEVELS,
  severityAtOrAbove,
} from '@/lib/logUtils'

type Tab = 'info' | 'services' | 'alerts' | 'logs' | 'tuning' | 'danger'

const TABS: { key: Tab; label: string }[] = [
  { key: 'info', label: 'Info' },
  { key: 'services', label: 'Service Handling' },
  { key: 'alerts', label: 'Alerts' },
  { key: 'logs', label: 'System Logs' },
  { key: 'tuning', label: 'System' },
  { key: 'danger', label: 'Danger Zone' },
]

export default function SettingsPage() {
  const [tab, setTab] = useState<Tab>('info')
  const fetcher = useCallback(() => api.settings(), [])
  const { data: settings, error, loading, refresh } = useApi(fetcher)
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState('')

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!settings) return null

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Settings</h1>

      <div className="flex gap-1 mb-6 border-b border-border overflow-x-auto">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => { setTab(t.key); setMsg('') }}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${
              tab === t.key
                ? 'border-accent text-accent'
                : 'border-transparent text-gray-400 hover:text-gray-200'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {msg && (
        <div className={`mb-4 px-4 py-2 rounded text-sm ${
          msg.startsWith('Error') ? 'bg-red-900/30 text-red-400' : 'bg-green-900/30 text-green-400'
        }`}>
          {msg}
        </div>
      )}

      {tab === 'info' && <SystemInfoTab />}
      {tab === 'services' && (
        <ServiceHandlingTab
          settings={settings}
          saving={saving}
          onSave={async (patch) => {
            setSaving(true); setMsg('')
            try {
              await api.updateSettings(patch)
              setMsg('Settings saved')
              refresh()
            } catch (e: any) { setMsg(`Error: ${e.message}`) }
            finally { setSaving(false) }
          }}
        />
      )}
      {tab === 'alerts' && (
        <AlertsTab
          settings={settings}
          saving={saving}
          onSave={async (patch) => {
            setSaving(true); setMsg('')
            try {
              await api.updateSettings(patch)
              setMsg('Settings saved')
              refresh()
            } catch (e: any) { setMsg(`Error: ${e.message}`) }
            finally { setSaving(false) }
          }}
        />
      )}
      {tab === 'logs' && <SystemLogsTab />}
      {tab === 'tuning' && <SystemTuningTab setMsg={setMsg} />}
      {tab === 'danger' && <DangerZoneTab setMsg={setMsg} />}
    </div>
  )
}

function ServiceHandlingTab({ settings, saving, onSave }: {
  settings: Settings; saving: boolean; onSave: (patch: Partial<Settings>) => void
}) {
  const [restartCount, setRestartCount] = useState(settings.restart_loop_count)
  const [windowMin, setWindowMin] = useState(settings.restart_loop_window_min)
  const [maxRetries, setMaxRetries] = useState(settings.restart_loop_max_retries)
  const [depMode, setDepMode] = useState(settings.dep_handling_mode)
  const [admissionDiskMinGB, setAdmissionDiskMinGB] = useState(settings.admission_disk_min_gb)
  const [admissionTempMax, setAdmissionTempMax] = useState(settings.admission_temp_max)

  const changed = restartCount !== settings.restart_loop_count
    || windowMin !== settings.restart_loop_window_min
    || maxRetries !== settings.restart_loop_max_retries
    || depMode !== settings.dep_handling_mode
    || admissionDiskMinGB !== settings.admission_disk_min_gb
    || admissionTempMax !== settings.admission_temp_max

  return (
    <div className="space-y-6">
      <Card>
        <CardTitle>Restart Loop Detection</CardTitle>
        <p className="text-sm text-gray-400 mb-4">
          Detect containers stuck in restart loops and optionally auto-stop them.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <div>
            <label className="block text-sm text-gray-300 mb-1">Alert after N restarts</label>
            <input
              type="number" min={1} max={100}
              value={restartCount}
              onChange={(e) => setRestartCount(Number(e.target.value))}
              className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
            />
            <p className="text-xs text-gray-500 mt-1">Fire critical alert after this many restarts</p>
          </div>
          <div>
            <label className="block text-sm text-gray-300 mb-1">Within M minutes</label>
            <input
              type="number" min={1} max={60}
              value={windowMin}
              onChange={(e) => setWindowMin(Number(e.target.value))}
              className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
            />
            <p className="text-xs text-gray-500 mt-1">Time window for counting restarts</p>
          </div>
          <div>
            <label className="block text-sm text-gray-300 mb-1">Max retries before auto-stop</label>
            <input
              type="number" min={0} max={1000}
              value={maxRetries}
              onChange={(e) => setMaxRetries(Number(e.target.value))}
              className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
            />
            <p className="text-xs text-gray-500 mt-1">0 = never auto-stop</p>
          </div>
        </div>
      </Card>

      <Card>
        <CardTitle>Dependent Service Handling</CardTitle>
        <p className="text-sm text-gray-400 mb-4">
          How to handle services when their upstream dependency is unhealthy.
        </p>
        <div className="space-y-2">
          <label className="flex items-center gap-3 cursor-pointer">
            <input
              type="radio" name="dep_mode" value="flag_only"
              checked={depMode === 'flag_only'}
              onChange={() => setDepMode('flag_only')}
              className="accent-accent"
            />
            <div>
              <span className="text-sm text-white font-medium">Flag only</span>
              <p className="text-xs text-gray-500">Show warning but keep dependent services running</p>
            </div>
          </label>
          <label className="flex items-center gap-3 cursor-pointer">
            <input
              type="radio" name="dep_mode" value="flag_and_stop"
              checked={depMode === 'flag_and_stop'}
              onChange={() => setDepMode('flag_and_stop')}
              className="accent-accent"
            />
            <div>
              <span className="text-sm text-white font-medium">Flag and auto-stop</span>
              <p className="text-xs text-gray-500">Show warning and automatically stop dependent services</p>
            </div>
          </label>
        </div>
      </Card>

      <Card>
        <CardTitle>Admission Control</CardTitle>
        <p className="text-sm text-gray-400 mb-4">
          Block manual service starts when system resources are stressed. Does not affect Docker restart policies.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 max-w-md">
          <div>
            <label className="block text-sm text-gray-300 mb-1">Min free disk (GB)</label>
            <div className="flex items-center gap-2">
              <input
                type="number" min={1} max={500} step={1}
                value={admissionDiskMinGB}
                onChange={(e) => setAdmissionDiskMinGB(Number(e.target.value))}
                className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
              />
              <span className="text-sm text-gray-400">GB</span>
            </div>
            <p className="text-xs text-gray-500 mt-1">Refuse service start if free disk is below this</p>
          </div>
          <div>
            <label className="block text-sm text-gray-300 mb-1">Max temperature</label>
            <div className="flex items-center gap-2">
              <input
                type="number" min={50} max={100} step={1}
                value={admissionTempMax}
                onChange={(e) => setAdmissionTempMax(Number(e.target.value))}
                className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
              />
              <span className="text-sm text-gray-400">&deg;C</span>
            </div>
            <p className="text-xs text-gray-500 mt-1">Refuse service start if CPU temp is at or above this</p>
          </div>
        </div>
      </Card>

      <div className="flex justify-end">
        <button
          disabled={!changed || saving}
          onClick={() => onSave({
            restart_loop_count: restartCount,
            restart_loop_window_min: windowMin,
            restart_loop_max_retries: maxRetries,
            dep_handling_mode: depMode,
            admission_disk_min_gb: admissionDiskMinGB,
            admission_temp_max: admissionTempMax,
          })}
          className="px-4 py-2 bg-accent text-black font-medium rounded text-sm hover:bg-accent/90 transition-colors disabled:opacity-50"
        >
          {saving ? 'Saving...' : 'Save Changes'}
        </button>
      </div>
    </div>
  )
}

function AlertsTab({ settings, saving, onSave }: {
  settings: Settings; saving: boolean; onSave: (patch: Partial<Settings>) => void
}) {
  const [tempWarning, setTempWarning] = useState(settings.temp_warning)
  const [tempCritical, setTempCritical] = useState(settings.temp_critical)

  const changed = tempWarning !== settings.temp_warning || tempCritical !== settings.temp_critical

  return (
    <div className="space-y-6">
      <Card>
        <CardTitle>Temperature Thresholds</CardTitle>
        <p className="text-sm text-gray-400 mb-4">
          Configure when temperature alerts fire. Values in degrees Celsius.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 max-w-md">
          <div>
            <label className="block text-sm text-gray-300 mb-1">Warning threshold</label>
            <div className="flex items-center gap-2">
              <input
                type="number" min={50} max={90} step={1}
                value={tempWarning}
                onChange={(e) => setTempWarning(Number(e.target.value))}
                className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
              />
              <span className="text-sm text-gray-400">&deg;C</span>
            </div>
          </div>
          <div>
            <label className="block text-sm text-gray-300 mb-1">Critical threshold</label>
            <div className="flex items-center gap-2">
              <input
                type="number" min={50} max={100} step={1}
                value={tempCritical}
                onChange={(e) => setTempCritical(Number(e.target.value))}
                className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
              />
              <span className="text-sm text-gray-400">&deg;C</span>
            </div>
          </div>
        </div>
        {tempWarning >= tempCritical && (
          <p className="text-sm text-yellow-400 mt-3">Warning threshold should be lower than critical threshold.</p>
        )}
      </Card>

      <div className="flex justify-end">
        <button
          disabled={!changed || saving || tempWarning >= tempCritical}
          onClick={() => onSave({ temp_warning: tempWarning, temp_critical: tempCritical })}
          className="px-4 py-2 bg-accent text-black font-medium rounded text-sm hover:bg-accent/90 transition-colors disabled:opacity-50"
        >
          {saving ? 'Saving...' : 'Save Changes'}
        </button>
      </div>
    </div>
  )
}

// --- System Logs Tab ---

type SeverityFilter = 'all' | LogSeverity

const UNIT_OPTIONS = [
  { value: '', label: 'All' },
  { value: 'docker', label: 'Docker' },
  { value: 'kernel', label: 'Kernel' },
  { value: 'systemd', label: 'systemd' },
  { value: 'nftables', label: 'nftables' },
  { value: 'ssh', label: 'SSH' },
] as const

function SystemInfoTab() {
  const fetcher = useCallback(() => api.systemInfo(), [])
  const { data, error, loading } = useApi(fetcher)

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!data) return null

  const Row = ({ label, value }: { label: string; value: string | number }) => (
    <div className="flex justify-between py-1.5 border-b border-border-subtle last:border-0">
      <span className="text-gray-500 text-sm">{label}</span>
      <span className="text-gray-300 text-sm font-mono">{value}</span>
    </div>
  )

  return (
    <div className="space-y-4">
      <Card>
        <CardTitle>Hardware</CardTitle>
        <div className="divide-y divide-border-subtle">
          <Row label="Model" value={data.model} />
          <Row label="CPU Cores" value={data.cpu_cores} />
          <Row label="Memory Total" value={data.mem_total} />
          <Row label="Memory Available" value={data.mem_free} />
        </div>
      </Card>
      <Card>
        <CardTitle>System</CardTitle>
        <div className="divide-y divide-border-subtle">
          <Row label="Hostname" value={data.hostname} />
          <Row label="OS" value={data.os} />
          <Row label="Kernel" value={data.kernel} />
          <Row label="Uptime" value={data.uptime} />
        </div>
      </Card>
      {data.storage.length > 0 && (
        <Card>
          <CardTitle>Storage</CardTitle>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-gray-500 border-b border-border-subtle">
                  <th className="pb-2 pr-4">Mount</th>
                  <th className="pb-2 pr-4">Device</th>
                  <th className="pb-2 pr-4">Type</th>
                  <th className="pb-2 pr-4 text-right">Size</th>
                  <th className="pb-2 pr-4 text-right">Used</th>
                  <th className="pb-2 text-right">Free</th>
                  <th className="pb-2 pl-4 text-right">Use%</th>
                </tr>
              </thead>
              <tbody>
                {data.storage.map((s) => (
                  <tr key={s.mount} className="border-b border-border-subtle last:border-0">
                    <td className="py-1.5 pr-4 font-mono text-gray-300">{s.mount}</td>
                    <td className="py-1.5 pr-4 font-mono text-gray-400 text-xs">{s.device}</td>
                    <td className="py-1.5 pr-4 text-gray-400">{s.fstype}</td>
                    <td className="py-1.5 pr-4 text-right text-gray-300">{s.size}</td>
                    <td className="py-1.5 pr-4 text-right text-gray-300">{s.used}</td>
                    <td className="py-1.5 text-right text-gray-300">{s.free}</td>
                    <td className="py-1.5 pl-4 text-right text-gray-300">{s.use_pct}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      {data.networks.length > 0 && (
        <Card>
          <CardTitle>Network</CardTitle>
          <div className="space-y-3">
            {data.networks.map((net) => (
              <div key={net.name} className="divide-y divide-border-subtle">
                <div className="flex justify-between py-1.5">
                  <span className="text-gray-500 text-sm">Interface</span>
                  <span className="text-accent text-sm font-mono">{net.name}</span>
                </div>
                <Row label="IP" value={net.ip} />
                <Row label="MAC" value={net.mac} />
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  )
}

const LINE_OPTIONS = [50, 100, 200, 500] as const

function SystemLogsTab() {
  const [lines, setLines] = useState(200)
  const [unit, setUnit] = useState('')
  const [priority, setPriority] = useState('')
  const [boot, setBoot] = useState(0)
  const [filter, setFilter] = useState<SeverityFilter>('all')
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [since, setSince] = useState('')
  const logEndRef = useRef<HTMLDivElement>(null)

  const bootsFetcher = useCallback(() => api.systemTuning(), [])
  const { data: tuningData } = useApi(bootsFetcher)
  const boots = tuningData?.boots ?? []

  const fetcher = useCallback(
    () => api.systemJournal(lines, priority, unit, since, boot),
    [lines, priority, unit, since, boot],
  )
  const { data, error, loading, refresh } = useApi(fetcher, autoRefresh ? 10000 : 0)

  const handleClear = () => {
    setSince(new Date().toISOString())
    setFilter('all')
  }

  const parsed = useMemo(() => {
    if (!data?.logs) return []
    return data.logs.split('\n').filter(Boolean).map((line) => ({
      text: line,
      severity: classifyLine(line),
    }))
  }, [data])

  const errorCount = useMemo(() => parsed.filter((l) => l.severity === 'error').length, [parsed])
  const warnCount = useMemo(() => parsed.filter((l) => l.severity === 'warn').length, [parsed])

  const filtered = useMemo(() => {
    if (filter === 'all') return parsed
    const allowed = severityAtOrAbove(filter)
    return parsed.filter((l) => allowed.has(l.severity) || l.severity === 'unknown')
  }, [parsed, filter])

  useEffect(() => {
    if (logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: 'smooth' })
    }
  }, [filtered])

  return (
    <div className="space-y-4">
      {/* Controls row */}
      <div className="flex flex-wrap items-center gap-3">
        <select
          value={unit}
          onChange={(e) => setUnit(e.target.value)}
          className="px-3 py-1.5 bg-surface-overlay border border-border rounded text-sm text-white"
        >
          {UNIT_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>

        <div className="flex gap-1">
          {([
            { key: 'all' as SeverityFilter, label: 'All' },
            { key: 'error' as SeverityFilter, label: errorCount > 0 ? `Error (${errorCount})` : 'Error' },
            { key: 'warn' as SeverityFilter, label: warnCount > 0 ? `Warn (${warnCount})` : 'Warn' },
            { key: 'info' as SeverityFilter, label: 'Info' },
            { key: 'debug' as SeverityFilter, label: 'Debug' },
          ]).map((fb) => (
            <button
              key={fb.key}
              onClick={() => setFilter(fb.key)}
              className={`px-2 py-1 text-xs rounded font-medium transition-colors ${
                filter === fb.key
                  ? 'bg-accent text-black'
                  : fb.key === 'error' && errorCount > 0
                    ? 'bg-surface-overlay text-red-400 hover:text-white'
                    : fb.key === 'warn' && warnCount > 0
                      ? 'bg-surface-overlay text-yellow-400 hover:text-white'
                      : 'bg-surface-overlay text-gray-400 hover:text-white'
              }`}
            >
              {fb.label}
            </button>
          ))}
        </div>

        <select
          value={lines}
          onChange={(e) => setLines(Number(e.target.value))}
          className="px-3 py-1.5 bg-surface-overlay border border-border rounded text-sm text-white"
        >
          {LINE_OPTIONS.map((n) => (
            <option key={n} value={n}>{n} lines</option>
          ))}
        </select>

        <select
          value={boot}
          onChange={(e) => setBoot(Number(e.target.value))}
          className="px-3 py-1.5 bg-surface-overlay border border-border rounded text-sm text-white"
        >
          {boots.length > 0 ? boots.map((b) => (
            <option key={b.index} value={b.index}>
              {b.index === 0 ? 'Current' : `Boot ${b.index}`} ({b.first})
            </option>
          )) : (
            <option value={0}>Current boot</option>
          )}
        </select>

        <label className="flex items-center gap-1.5 text-sm text-gray-400 cursor-pointer">
          <input
            type="checkbox" checked={autoRefresh} onChange={(e) => setAutoRefresh(e.target.checked)}
            className="accent-accent"
          />
          Auto-refresh
        </label>

        <button
          onClick={handleClear}
          className="px-2 py-1 text-xs bg-surface-overlay text-gray-400 hover:text-white rounded transition-colors"
        >
          Clear
        </button>

        <button
          onClick={refresh}
          disabled={loading}
          className="px-2 py-1 text-xs bg-surface-overlay text-gray-400 hover:text-white rounded transition-colors disabled:opacity-50"
        >
          Refresh
        </button>
      </div>

      {error && <div className="text-red-400 text-sm">Error: {error}</div>}

      {/* Log output */}
      <div className="bg-surface-overlay border border-border rounded p-3 font-mono text-xs max-h-[600px] overflow-y-auto">
        {loading && !data ? (
          <div className="text-gray-500">Loading...</div>
        ) : filtered.length === 0 ? (
          <div className="text-gray-500">{boot < 0 ? 'No logs available for this boot' : 'No log entries'}</div>
        ) : (
          filtered.map((line, i) => (
            <div key={i} className={`${severityColor[line.severity]} whitespace-pre-wrap`}>
              {line.text}
            </div>
          ))
        )}
        <div ref={logEndRef} />
      </div>
    </div>
  )
}

// --- System Tuning Tab ---

function SystemTuningTab({ setMsg }: { setMsg: (m: string) => void }) {
  const fetcher = useCallback(() => api.systemTuning(), [])
  const { data: tuning, error, loading, refresh } = useApi(fetcher)
  const [swappiness, setSwappiness] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [confirmDisableJournal, setConfirmDisableJournal] = useState(false)

  useEffect(() => {
    if (tuning && swappiness === null) {
      setSwappiness(tuning.swappiness)
    }
  }, [tuning, swappiness])

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!tuning) return null

  async function toggleJournal(enable: boolean) {
    setSaving(true); setMsg('')
    try {
      await api.setSystemTuning('set_persistent_journal', String(enable))
      setMsg(enable ? 'Persistent journal enabled' : 'Persistent journal disabled')
      setConfirmDisableJournal(false)
      refresh()
    } catch (e: any) { setMsg(`Error: ${e.message}`) }
    finally { setSaving(false) }
  }

  async function saveSwappiness() {
    if (swappiness === null) return
    setSaving(true); setMsg('')
    try {
      await api.setSystemTuning('set_swappiness', String(swappiness))
      setMsg('Swappiness updated')
      refresh()
    } catch (e: any) { setMsg(`Error: ${e.message}`) }
    finally { setSaving(false) }
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardTitle>Persistent Journal</CardTitle>
        <p className="text-sm text-gray-400 mb-4">
          When enabled, system logs survive reboots by writing to /var/log/journal/ on disk.
          Without this, logs are lost on every restart and crash investigation becomes impossible.
        </p>
        <div className="flex items-center gap-4 mb-3">
          <span className="text-sm text-gray-300">Status:</span>
          <span className={`text-sm font-medium ${tuning.persistent_journal ? 'text-green-400' : 'text-yellow-400'}`}>
            {tuning.persistent_journal ? 'Enabled' : 'Disabled'}
          </span>
          {tuning.persistent_journal && tuning.journal_disk_usage && (
            <span className="text-xs text-gray-500">({tuning.journal_disk_usage} on disk)</span>
          )}
        </div>
        {tuning.persistent_journal ? (
          confirmDisableJournal ? (
            <div className="flex items-center gap-2">
              <span className="text-sm text-red-400">This will delete all stored logs. Continue?</span>
              <button
                onClick={() => toggleJournal(false)}
                disabled={saving}
                className="px-3 py-1.5 bg-red-600 hover:bg-red-700 text-white text-sm font-medium rounded disabled:opacity-50"
              >
                {saving ? 'Disabling...' : 'Confirm Disable'}
              </button>
              <button
                onClick={() => setConfirmDisableJournal(false)}
                className="px-3 py-1.5 text-sm text-gray-400 hover:text-gray-200"
              >
                Cancel
              </button>
            </div>
          ) : (
            <button
              onClick={() => setConfirmDisableJournal(true)}
              className="px-4 py-2 bg-red-600/20 text-red-400 hover:bg-red-600/30 text-sm font-medium rounded transition-colors"
            >
              Disable Persistent Journal
            </button>
          )
        ) : (
          <button
            onClick={() => toggleJournal(true)}
            disabled={saving}
            className="px-4 py-2 bg-green-600 hover:bg-green-700 text-white text-sm font-medium rounded transition-colors disabled:opacity-50"
          >
            {saving ? 'Enabling...' : 'Enable Persistent Journal'}
          </button>
        )}
      </Card>

      <Card>
        <CardTitle>Swappiness</CardTitle>
        <p className="text-sm text-gray-400 mb-4">
          Controls how aggressively the kernel swaps memory pages to disk.
          Lower values prefer keeping data in RAM, higher values prefer swap.
          Default is 60. Recommended <strong className="text-gray-300">10</strong> for this workload
          (Bitcoin services benefit from hot caches).
        </p>
        <div className="flex items-center gap-3 max-w-xs">
          <input
            type="number" min={0} max={100}
            value={swappiness ?? tuning.swappiness}
            onChange={(e) => setSwappiness(Number(e.target.value))}
            className="w-24 px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white"
          />
          <span className="text-xs text-gray-500">0 = never swap, 100 = swap aggressively</span>
        </div>
        <div className="flex items-center gap-3 mt-4">
          <button
            disabled={saving || swappiness === tuning.swappiness}
            onClick={saveSwappiness}
            className="px-4 py-2 bg-accent text-black font-medium rounded text-sm hover:bg-accent/90 transition-colors disabled:opacity-50"
          >
            {saving ? 'Saving...' : 'Save'}
          </button>
          <span className="text-xs text-gray-500">
            Current: {tuning.swappiness}
          </span>
        </div>
      </Card>
    </div>
  )
}

// --- Reboot Overlay ---

function RebootOverlay({ action }: { action: 'shutdown' | 'restart' }) {
  const [elapsed, setElapsed] = useState(0)
  const [status, setStatus] = useState<'waiting' | 'polling' | 'online'>('waiting')
  const isShutdown = action === 'shutdown'

  useEffect(() => {
    if (isShutdown) return
    const timer = setInterval(() => setElapsed((s) => s + 1), 1000)
    return () => clearInterval(timer)
  }, [isShutdown])

  useEffect(() => {
    if (isShutdown) return
    if (elapsed < 15) return
    if (status === 'online') return
    if (status === 'waiting') setStatus('polling')

    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 3000)
    fetch('/api/truffels/health', { signal: controller.signal })
      .then((r) => { if (r.ok) setStatus('online') })
      .catch(() => {})
      .finally(() => clearTimeout(timeout))
  }, [elapsed, isShutdown, status])

  useEffect(() => {
    if (status === 'online') {
      const t = setTimeout(() => { window.location.href = '/admin/' }, 1500)
      return () => clearTimeout(t)
    }
  }, [status])

  const minutes = Math.floor(elapsed / 60)
  const seconds = elapsed % 60
  const timeStr = `${minutes}:${seconds.toString().padStart(2, '0')}`

  if (isShutdown) {
    return (
      <div className="fixed inset-0 z-50 bg-black/95 flex items-center justify-center">
        <div className="text-center space-y-6 max-w-md">
          <div className="text-6xl text-red-500">{'\u23FB'}</div>
          <h2 className="text-2xl font-bold text-white">System Shutting Down</h2>
          <p className="text-gray-400">
            The system is powering off. You can close this page.
          </p>
          <p className="text-gray-400">
            You may need to physically power the device back on.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="fixed inset-0 z-50 bg-black/95 flex items-center justify-center">
      <div className="text-center space-y-6 max-w-md">
        <div className={`text-6xl ${status === 'online' ? 'text-green-500' : 'text-yellow-500'}`}>
          {status === 'online' ? '\u2713' : '\u23F3'}
        </div>
        <h2 className="text-2xl font-bold text-white">
          {status === 'online' ? 'System Online' : 'System Restarting'}
        </h2>
        <p className="text-gray-400">
          {status === 'online'
            ? 'Redirecting to login...'
            : status === 'polling'
              ? 'Waiting for system to come back online...'
              : 'System is going down for restart...'}
        </p>
        <div className="text-4xl font-mono text-gray-300">{timeStr}</div>
        {status === 'polling' && (
          <div className="flex justify-center">
            <div className="w-8 h-8 border-2 border-yellow-500 border-t-transparent rounded-full animate-spin" />
          </div>
        )}
      </div>
    </div>
  )
}

function DangerZoneTab({ setMsg }: { setMsg: (m: string) => void }) {
  const [password, setPassword] = useState('')
  const [confirming, setConfirming] = useState<'shutdown' | 'restart' | null>(null)
  const [running, setRunning] = useState(false)
  const [overlay, setOverlay] = useState<'shutdown' | 'restart' | null>(null)

  async function handleAction(action: 'shutdown' | 'restart') {
    if (!password) { setMsg('Error: Password required'); return }
    setRunning(true); setMsg('')
    try {
      if (action === 'shutdown') {
        await api.systemShutdown(password)
      } else {
        await api.systemRestart(password)
      }
      // Clear session so redirect lands on login screen
      try { await fetch('/api/truffels/auth/logout', { method: 'POST' }) } catch {}
      setOverlay(action)
    } catch (e: any) {
      setMsg(`Error: ${e.message}`)
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="space-y-6">
      {overlay && <RebootOverlay action={overlay} />}
      <Card className="border-red-900/50">
        <CardTitle>System Power</CardTitle>
        <p className="text-sm text-gray-400 mb-4">
          Shutdown or restart the entire Truffels system. All services will be stopped.
          This requires your admin password for confirmation.
        </p>

        <div className="max-w-sm mb-4">
          <label className="block text-sm text-gray-300 mb-1">Admin password</label>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Enter password to confirm"
            className="w-full px-3 py-2 bg-surface-overlay border border-border rounded text-sm text-white placeholder-gray-600"
          />
        </div>

        <div className="flex gap-3">
          {confirming === 'restart' ? (
            <div className="flex items-center gap-2">
              <span className="text-sm text-yellow-400">Restart the system?</span>
              <button
                onClick={() => handleAction('restart')}
                disabled={running}
                className="px-3 py-1.5 bg-yellow-600 hover:bg-yellow-700 text-white text-sm font-medium rounded disabled:opacity-50"
              >
                {running ? 'Restarting...' : 'Confirm Restart'}
              </button>
              <button
                onClick={() => setConfirming(null)}
                className="px-3 py-1.5 text-sm text-gray-400 hover:text-gray-200"
              >
                Cancel
              </button>
            </div>
          ) : (
            <button
              onClick={() => setConfirming('restart')}
              disabled={!password}
              className="px-4 py-2 bg-yellow-600 hover:bg-yellow-700 text-white text-sm font-medium rounded transition-colors disabled:opacity-50"
            >
              Restart System
            </button>
          )}

          {confirming === 'shutdown' ? (
            <div className="flex items-center gap-2">
              <span className="text-sm text-red-400">Shutdown the system?</span>
              <button
                onClick={() => handleAction('shutdown')}
                disabled={running}
                className="px-3 py-1.5 bg-red-600 hover:bg-red-700 text-white text-sm font-medium rounded disabled:opacity-50"
              >
                {running ? 'Shutting down...' : 'Confirm Shutdown'}
              </button>
              <button
                onClick={() => setConfirming(null)}
                className="px-3 py-1.5 text-sm text-gray-400 hover:text-gray-200"
              >
                Cancel
              </button>
            </div>
          ) : (
            <button
              onClick={() => setConfirming('shutdown')}
              disabled={!password}
              className="px-4 py-2 bg-red-600 hover:bg-red-700 text-white text-sm font-medium rounded transition-colors disabled:opacity-50"
            >
              Shutdown System
            </button>
          )}
        </div>
      </Card>
    </div>
  )
}
