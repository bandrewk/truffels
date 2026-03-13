import { useCallback, useEffect, useState } from 'react'
import { api, Settings } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card, CardTitle } from '@/components/Card'

type Tab = 'services' | 'alerts' | 'danger'

const TABS: { key: Tab; label: string }[] = [
  { key: 'services', label: 'Service Handling' },
  { key: 'alerts', label: 'Alerts' },
  { key: 'danger', label: 'Danger Zone' },
]

export default function SettingsPage() {
  const [tab, setTab] = useState<Tab>('services')
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

      <div className="flex gap-1 mb-6 border-b border-border">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => { setTab(t.key); setMsg('') }}
            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
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
              <span className="text-sm text-gray-400">°C</span>
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
              <span className="text-sm text-gray-400">°C</span>
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

function RebootOverlay({ action }: { action: 'shutdown' | 'restart' }) {
  const [elapsed, setElapsed] = useState(0)
  const [status, setStatus] = useState<'waiting' | 'polling' | 'online'>('waiting')

  useEffect(() => {
    const timer = setInterval(() => setElapsed((s) => s + 1), 1000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    if (action === 'shutdown') return
    // Start polling after 15s (system needs time to go down first)
    if (elapsed < 15) return
    if (status === 'online') return
    if (status === 'waiting') setStatus('polling')

    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 3000)
    fetch('/api/truffels/health', { signal: controller.signal })
      .then((r) => { if (r.ok) setStatus('online') })
      .catch(() => {})
      .finally(() => clearTimeout(timeout))
  }, [elapsed, action, status])

  useEffect(() => {
    if (status === 'online') {
      const t = setTimeout(() => { window.location.href = '/admin/' }, 1500)
      return () => clearTimeout(t)
    }
  }, [status])

  const isShutdown = action === 'shutdown'
  const minutes = Math.floor(elapsed / 60)
  const seconds = elapsed % 60
  const timeStr = `${minutes}:${seconds.toString().padStart(2, '0')}`

  return (
    <div className="fixed inset-0 z-50 bg-black/95 flex items-center justify-center">
      <div className="text-center space-y-6 max-w-md">
        <div className={`text-6xl ${isShutdown ? 'text-red-500' : 'text-yellow-500'}`}>
          {status === 'online' ? '\u2713' : '\u23F3'}
        </div>
        <h2 className="text-2xl font-bold text-white">
          {isShutdown ? 'System Shutting Down' : status === 'online' ? 'System Online' : 'System Restarting'}
        </h2>
        <p className="text-gray-400">
          {isShutdown
            ? 'The system is powering off. You can close this page.'
            : status === 'online'
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
        {isShutdown && elapsed > 10 && (
          <p className="text-sm text-gray-600">You may need to physically power the device back on.</p>
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
