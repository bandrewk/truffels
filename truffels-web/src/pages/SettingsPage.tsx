import { useCallback, useState } from 'react'
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

  const changed = restartCount !== settings.restart_loop_count
    || windowMin !== settings.restart_loop_window_min
    || maxRetries !== settings.restart_loop_max_retries
    || depMode !== settings.dep_handling_mode

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

      <div className="flex justify-end">
        <button
          disabled={!changed || saving}
          onClick={() => onSave({
            restart_loop_count: restartCount,
            restart_loop_window_min: windowMin,
            restart_loop_max_retries: maxRetries,
            dep_handling_mode: depMode,
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

function DangerZoneTab({ setMsg }: { setMsg: (m: string) => void }) {
  const [password, setPassword] = useState('')
  const [confirming, setConfirming] = useState<'shutdown' | 'restart' | null>(null)
  const [running, setRunning] = useState(false)

  async function handleAction(action: 'shutdown' | 'restart') {
    if (!password) { setMsg('Error: Password required'); return }
    setRunning(true); setMsg('')
    try {
      if (action === 'shutdown') {
        await api.systemShutdown(password)
      } else {
        await api.systemRestart(password)
      }
      setMsg(`System ${action} initiated. The system will be unavailable shortly.`)
      setConfirming(null)
      setPassword('')
    } catch (e: any) {
      setMsg(`Error: ${e.message}`)
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="space-y-6">
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
