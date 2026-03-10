const BASE = '/api/truffels'

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`)
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  return res.json()
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.error || `HTTP ${res.status}`)
  }
  return res.json()
}

export interface HostMetrics {
  cpu_percent: number
  mem_total_mb: number
  mem_used_mb: number
  mem_percent: number
  temperature_c: number
  disks: { path: string; total_gb: number; used_gb: number; avail_gb: number; used_percent: number }[]
  uptime_seconds: number
}

export interface ContainerState {
  name: string
  status: string
  health: string
  restart_count: number
  started_at: string
  image: string
}

export interface ServiceTemplate {
  id: string
  display_name: string
  description: string
  container_names: string[]
  dependencies: string[] | null
  memory_limit: string
  port?: string
  read_only?: boolean
}

export interface ServiceInstance {
  template: ServiceTemplate
  state: 'running' | 'stopped' | 'degraded' | 'unknown'
  enabled: boolean
  containers: ContainerState[]
}

export interface Alert {
  id: number
  type: string
  severity: 'warning' | 'critical'
  service_id: string
  message: string
  first_seen: string
  last_seen: string
  resolved: boolean
  resolved_at?: string
}

export interface Dashboard {
  host: HostMetrics
  services: { id: string; display_name: string; state: string; containers: ContainerState[] }[]
  alerts: { active_count: number; recent: Alert[] }
}

export interface ConfigResponse {
  config: string | null
  path?: string
  revisions: { id: number; timestamp: string; actor: string; diff: string; config_snapshot: string }[]
  message?: string
}

export interface BitcoindStats {
  blockchain: {
    chain: string
    blocks: number
    headers: number
    bestblockhash: string
    difficulty: number
    verificationprogress: number
    size_on_disk: number
    pruned: boolean
  }
  network: {
    version: number
    subversion: string
    protocolversion: number
    connections: number
    connections_in: number
    connections_out: number
  }
  mempool: {
    size: number
    bytes: number
    usage: number
    total_fee: number
    mempoolminfee: number
    minrelaytxfee: number
  }
}

export const api = {
  dashboard: () => get<Dashboard>('/dashboard'),
  services: () => get<ServiceInstance[]>('/services'),
  service: (id: string) => get<ServiceInstance>(`/services/${id}`),
  serviceAction: (id: string, action: string) => post<{ status: string }>(`/services/${id}/action`, { action }),
  serviceLogs: (id: string, tail = 200) => get<{ logs: string }>(`/services/${id}/logs?tail=${tail}`),
  serviceConfig: (id: string) => get<ConfigResponse>(`/services/${id}/config`),
  updateConfig: (id: string, config: string, restart: boolean) =>
    post<{ status: string }>(`/services/${id}/config`, { config, restart }),
  bitcoindStats: () => get<BitcoindStats>('/services/bitcoind/stats'),
  host: () => get<HostMetrics>('/host'),
  alerts: (all = false) => get<Alert[]>(`/alerts${all ? '?all=true' : ''}`),
}
