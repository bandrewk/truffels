import { useCallback } from 'react'
import { Link } from 'react-router-dom'
import { api } from '@/lib/api'
import { useApi } from '@/hooks/useApi'
import { Card } from '@/components/Card'
import StatusBadge from '@/components/StatusBadge'

export default function ServicesPage() {
  const fetcher = useCallback(() => api.services(), [])
  const { data, error, loading } = useApi(fetcher, 10000)

  if (loading) return <div className="text-gray-400">Loading...</div>
  if (error) return <div className="text-red-400">Error: {error}</div>
  if (!data) return null

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Services</h1>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {data.map((svc) => (
          <Link key={svc.template.id} to={`/services/${svc.template.id}`}>
            <Card className="hover:border-accent/30 transition-colors h-full">
              <div className="flex items-start justify-between mb-2">
                <h3 className="font-semibold text-gray-100">{svc.template.display_name}</h3>
                <StatusBadge status={svc.state} />
              </div>
              <p className="text-sm text-gray-400 mb-3">{svc.template.description}</p>
              <div className="flex flex-wrap gap-2 text-xs">
                {svc.template.port && (
                  <span className="px-2 py-0.5 rounded bg-surface-overlay text-gray-400">
                    {svc.template.port}
                  </span>
                )}
                <span className="px-2 py-0.5 rounded bg-surface-overlay text-gray-400">
                  {svc.template.memory_limit} mem
                </span>
                {svc.template.dependencies?.map((dep) => (
                  <span key={dep} className="px-2 py-0.5 rounded bg-accent/10 text-accent text-xs">
                    needs {dep}
                  </span>
                ))}
              </div>
              <div className="mt-3 pt-3 border-t border-border-subtle">
                {svc.containers.map((c) => (
                  <div key={c.name} className="flex items-center justify-between text-xs py-0.5">
                    <span className="text-gray-400 font-mono">{c.name}</span>
                    <StatusBadge status={c.health || c.status} />
                  </div>
                ))}
              </div>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  )
}
