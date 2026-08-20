import { useCallback, useState, useEffect } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, CatalogEntry, AdmissionDecision } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card } from '@/components/Card'
import ConfirmDialog from '@/components/ConfirmDialog'

export default function AddServicePage() {
  const navigate = useNavigate()
  const catalogFetcher = useCallback(() => api.catalog(), [])
  const { data, error, loading } = useApi(catalogFetcher, 0)

  // The installed services tell us which catalog entries are already present,
  // so their cards can show "Installed" instead of offering another install.
  const servicesFetcher = useCallback(() => api.services(), [])
  const { data: services, refresh: refreshServices } = useApi(servicesFetcher, 0)
  const installedIds = new Set((services ?? []).map(s => s.template.id))

  const [selectedEntry, setSelectedEntry] = useState<CatalogEntry | null>(null)
  const [installing, setInstalling] = useState(false)
  const [formData, setFormData] = useState<Record<string, unknown>>({})
  const [installed, setInstalled] = useState<string | null>(null)

  const [admission, setAdmission] = useState<AdmissionDecision | null>(null)
  const [admissionLoading, setAdmissionLoading] = useState(false)

  useEffect(() => {
    if (selectedEntry) {
      setAdmissionLoading(true)
      api.catalogAdmission(selectedEntry.id)
        .then(res => setAdmission(res))
        .catch(err => setAdmission({ allowed: false, reason: err.message, required_ram_mb: 0, usable_ram_mb: 0, required_disk_gb: 0, free_disk_gb: 0 }))
        .finally(() => setAdmissionLoading(false))

      const defaults: Record<string, unknown> = {}
      if (selectedEntry.params) {
        selectedEntry.params.forEach(p => { defaults[p.name] = p.default })
      }
      setFormData(defaults)
    } else {
      setAdmission(null)
    }
  }, [selectedEntry])

  const handleInstall = async () => {
    if (!selectedEntry) return
    setInstalling(true)
    try {
      await api.installCatalog(selectedEntry.id, formData)
      const name = selectedEntry.display_name
      setSelectedEntry(null)
      setInstalled(name)
      refreshServices()
    } catch (e) {
      alert(`Install failed: ${e instanceof Error ? e.message : String(e)}`)
    } finally {
      setInstalling(false)
    }
  }

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!data) return null

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <button onClick={() => navigate('/services')} className="text-gray-400 hover:text-gray-200">&larr;</button>
        <h1 className="text-2xl font-bold">Add Service</h1>
      </div>

      {installed && (
        <div className="p-4 rounded bg-green-500/20 text-green-300 text-sm flex items-center justify-between">
          <span>{installed} was installed. You can start it from <Link to="/services" className="underline font-medium">Services</Link>.</span>
          <button onClick={() => setInstalled(null)} className="text-green-400 hover:text-green-200 ml-4">&times;</button>
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {data.map(entry => {
          const alreadyInstalled = installedIds.has(entry.id)
          return (
            <Card key={entry.id} className="h-full flex flex-col justify-between">
              <div>
                <div className="flex items-start justify-between mb-2">
                  <h3 className="font-semibold text-gray-100">{entry.display_name}</h3>
                </div>
                <p className="text-sm text-gray-400 mb-3">{entry.description}</p>
                <div className="flex flex-wrap gap-2 text-xs mb-4">
                  {entry.role && <span className="px-2 py-0.5 rounded bg-surface-overlay text-gray-400">{entry.role}</span>}
                  {entry.chain && <span className="px-2 py-0.5 rounded bg-surface-overlay text-gray-400">{entry.chain}</span>}
                </div>
              </div>
              {alreadyInstalled ? (
                <span className="mt-auto px-4 py-2 text-green-400 text-sm font-medium w-max">Installed &#10003;</span>
              ) : (
                <button
                  onClick={() => setSelectedEntry(entry)}
                  className="mt-auto px-4 py-2 bg-accent/20 hover:bg-accent/30 text-accent rounded text-sm font-medium transition-colors w-max"
                >
                  Install...
                </button>
              )}
            </Card>
          )
        })}
      </div>

      {selectedEntry && (
        <ConfirmDialog
          open={true}
          title={`Install ${selectedEntry.display_name}`}
          onCancel={() => !installing && setSelectedEntry(null)}
          onConfirm={handleInstall}
          confirmLabel={installing ? 'Installing...' : 'Install'}
          confirmDisabled={installing || (!!admission && !admission.allowed) || admissionLoading}
        >
          <div className="space-y-4">
            {admissionLoading && <div className="text-sm text-gray-400">Checking requirements...</div>}
            {admission && !admission.allowed && (
              <div className="p-3 rounded bg-red-500/20 text-red-400 text-sm">
                Cannot install: {admission.reason}
              </div>
            )}
            {admission && admission.allowed && (
              <div className="p-3 rounded bg-green-500/20 text-green-400 text-sm">
                Fits: needs ~{admission.required_ram_mb} MB RAM ({admission.usable_ram_mb} MB available), {admission.required_disk_gb} GB disk ({admission.free_disk_gb} GB free).
              </div>
            )}

            {selectedEntry.params?.map(p => (
              <div key={p.name} className="flex flex-col gap-1">
                <label className="text-sm text-gray-300 font-medium">{p.name}</label>
                {p.description && <span className="text-xs text-gray-500">{p.description}</span>}
                {p.type === 'bool' ? (
                  <input
                    type="checkbox"
                    checked={!!formData[p.name]}
                    onChange={e => setFormData(f => ({ ...f, [p.name]: e.target.checked }))}
                    className="w-4 h-4 rounded border-gray-600 bg-surface text-accent focus:ring-accent/50"
                  />
                ) : p.enum ? (
                  <select
                    value={String(formData[p.name] ?? '')}
                    onChange={e => setFormData(f => ({ ...f, [p.name]: e.target.value }))}
                    className="bg-surface border border-border rounded px-3 py-1.5 text-sm focus:outline-none focus:border-accent text-gray-200"
                  >
                    {p.enum.map(o => <option key={o} value={o}>{o}</option>)}
                  </select>
                ) : p.type === 'int' ? (
                  <input
                    type="number"
                    min={p.min}
                    max={p.max}
                    value={String(formData[p.name] ?? '')}
                    onChange={e => setFormData(f => ({ ...f, [p.name]: parseInt(e.target.value) || 0 }))}
                    className="bg-surface border border-border rounded px-3 py-1.5 text-sm focus:outline-none focus:border-accent text-gray-200"
                  />
                ) : (
                  <input
                    type="text"
                    value={String(formData[p.name] ?? '')}
                    onChange={e => setFormData(f => ({ ...f, [p.name]: e.target.value }))}
                    className="bg-surface border border-border rounded px-3 py-1.5 text-sm focus:outline-none focus:border-accent text-gray-200"
                  />
                )}
              </div>
            ))}
          </div>
        </ConfirmDialog>
      )}
    </div>
  )
}
