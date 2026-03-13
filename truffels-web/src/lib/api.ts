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
  fan_rpm: number
  fan_percent: number
  disks: { path: string; total_gb: number; used_gb: number; avail_gb: number; used_percent: number }[]
  uptime_seconds: number
  net_rx_bytes: number
  net_tx_bytes: number
  disk_read_bytes: number
  disk_write_bytes: number
  disk_io_percent: number
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
  floating_tag?: boolean
  update_source?: UpdateSource
}

export interface ServiceInstance {
  template: ServiceTemplate
  state: 'running' | 'stopped' | 'degraded' | 'unknown' | 'disabled'
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

export interface CkpoolStats {
  status: {
    runtime: number
    lastupdate: number
    Users: number
    Workers: number
    Idle: number
  }
  hashrates: {
    hashrate1m: string
    hashrate5m: string
    hashrate15m: string
    hashrate1hr: string
    hashrate6hr: string
    hashrate1d: string
    hashrate7d: string
  }
  shares: {
    diff: number
    accepted: number
    rejected: number
    bestshare: number
    SPS1m: number
    SPS5m: number
    SPS15m: number
    SPS1h: number
  }
}

export interface ElectrsStats {
  index_height: number
}

export interface UpdateCheck {
  id: number
  service_id: string
  current_version: string
  latest_version: string
  has_update: boolean
  checked_at: string
  error?: string
}

export interface UpdateLog {
  id: number
  service_id: string
  from_version: string
  to_version: string
  status: 'pending' | 'pulling' | 'building' | 'restarting' | 'done' | 'failed' | 'rolled_back'
  started_at: string
  completed_at?: string
  error?: string
  rollback_version?: string
}

export interface PreflightCheck {
  name: string
  status: 'pass' | 'fail' | 'warn'
  message: string
  blocking: boolean
}

export interface PreflightResult {
  service_id: string
  from_version: string
  to_version: string
  can_proceed: boolean
  checks: PreflightCheck[]
}

export interface UpdateSource {
  type: 'dockerhub' | 'github' | 'bitbucket'
  images?: string[]
  repo?: string
  branch?: string
  needs_build: boolean
  tag_filter?: string
}

export interface FloatingService {
  id: string
  display_name: string
  image: string
  current_version: string
  started_at: string
}

export interface UpdateStatus {
  pending_count: number
  checks: UpdateCheck[]
  updating: Record<string, boolean>
  sources: Record<string, UpdateSource>
  floating_services?: FloatingService[]
}

export interface MonitoringContainer {
  name: string
  service_id: string
  display_name: string
  status: string
  health: string
  restart_count: number
  started_at: string
  image: string
}

export interface MetricSnapshot {
  id: number
  timestamp: string
  cpu_percent: number
  mem_percent: number
  temp_c: number
  disk_percent: number
  fan_rpm: number
  fan_percent: number
  net_rx_bytes: number
  net_tx_bytes: number
  disk_read_bytes: number
  disk_write_bytes: number
  disk_io_percent: number
}

export interface MetricsSummary {
  cpu_avg: number
  cpu_max: number
  mem_avg: number
  mem_max: number
  temp_avg: number
  temp_max: number
}

export interface ServiceEvent {
  id: number
  timestamp: string
  service_id: string
  container: string
  event_type: string
  from_state: string
  to_state: string
  message: string
}

export interface ContainerSnapshot {
  id: number
  timestamp: string
  container: string
  cpu_percent: number
  mem_usage_mb: number
  mem_limit_mb: number
  net_rx_bytes: number
  net_tx_bytes: number
  block_read_bytes: number
  block_write_bytes: number
}

export interface ContainerCurrentStats {
  name: string
  cpu_percent: number
  mem_usage_mb: number
  mem_limit_mb: number
  net_rx_bytes: number
  net_tx_bytes: number
  block_read_bytes: number
  block_write_bytes: number
}

export interface ServiceMonitoringResponse {
  service_id: string
  containers: string[]
  snapshots: ContainerSnapshot[]
  current: ContainerCurrentStats[]
}

export interface MonitoringResponse {
  containers: MonitoringContainer[]
  events: ServiceEvent[]
  metrics: {
    current: HostMetrics
    history: MetricSnapshot[]
    summary: MetricsSummary
  }
  alerts: Alert[]
}

export interface Settings {
  restart_loop_count: number
  restart_loop_window_min: number
  restart_loop_max_retries: number
  dep_handling_mode: string
  temp_warning: number
  temp_critical: number
  admission_disk_min_gb: number
  admission_temp_max: number
}

async function put<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.error || `HTTP ${res.status}`)
  }
  return res.json()
}

export const api = {
  dashboard: () => get<Dashboard>('/dashboard'),
  services: () => get<ServiceInstance[]>('/services'),
  service: (id: string) => get<ServiceInstance>(`/services/${id}`),
  serviceAction: (id: string, action: string) => post<{ status: string }>(`/services/${id}/action`, { action }),
  serviceLogs: (id: string, tail = 200, since = '') => {
    const params = new URLSearchParams({ tail: String(tail) })
    if (since) params.set('since', since)
    return get<{ logs: string }>(`/services/${id}/logs?${params}`)
  },
  serviceConfig: (id: string) => get<ConfigResponse>(`/services/${id}/config`),
  updateConfig: (id: string, config: string, restart: boolean) =>
    post<{ status: string }>(`/services/${id}/config`, { config, restart }),
  bitcoindStats: () => get<BitcoindStats>('/services/bitcoind/stats'),
  ckpoolStats: () => get<CkpoolStats>('/services/ckpool/stats'),
  electrsStats: () => get<ElectrsStats>('/services/electrs/stats'),
  host: () => get<HostMetrics>('/host'),
  alerts: (all = false) => get<Alert[]>(`/alerts${all ? '?all=true' : ''}`),
  updates: () => get<UpdateCheck[]>('/updates'),
  updateStatus: () => get<UpdateStatus>('/updates/status'),
  checkUpdates: () => post<{ status: string }>('/updates/check'),
  applyUpdate: (id: string) => post<{ status: string }>(`/updates/apply/${id}`),
  applyAllUpdates: () => post<{ status: string; queued: string[] }>('/updates/apply-all'),
  updatePreflight: (id: string) => get<PreflightResult>(`/updates/preflight/${id}`),
  updateLogs: (serviceId?: string) => get<UpdateLog[]>(`/updates/logs${serviceId ? `?service=${serviceId}` : ''}`),
  rollbackService: (id: string) => post<{ status: string }>(`/updates/rollback/${id}`),
  monitoring: (hours = 24) => get<MonitoringResponse>(`/monitoring?hours=${hours}`),
  serviceMonitoring: (id: string, hours = 24) => get<ServiceMonitoringResponse>(`/services/${id}/monitoring?hours=${hours}`),
  settings: () => get<Settings>('/settings'),
  updateSettings: (settings: Partial<Settings>) => put<{ status: string }>('/settings', settings),
  systemRestart: (password: string) => post<{ status: string }>('/system/restart', { password }),
  systemShutdown: (password: string) => post<{ status: string }>('/system/shutdown', { password }),
}
