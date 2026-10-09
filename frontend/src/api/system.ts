import { api } from './client'

export type UpdateAgentStatus = {
  protocol_version?: number
  available: boolean
  staged_update?: boolean
  state: string
  job_id?: string
  current_version?: string
  target_version?: string
  backup_path?: string
  error?: string
  started_at?: string
  finished_at?: string
}

export type UpdatePreparationStatus = {
  job_id: string
  agent_job_id?: string
  state: string
  current_version?: string
  target_version?: string
  backup_path?: string
  error?: string
  started_at?: string
  finished_at?: string
}

export type SystemUpdateInfo = {
  current_version: string
  next_version?: string
  skipped_versions?: string[]
  rollback_versions?: Array<{
    tag_name: string
    name?: string
    body?: string
    published_at?: string
    html_url?: string
  }>
  recent_releases?: Array<{
    tag_name: string
    name?: string
    body?: string
    published_at?: string
    html_url?: string
  }>
  has_update: boolean
  managed: boolean
  cached: boolean
  warning?: string
  release?: {
    tag_name: string
    name?: string
    body?: string
    published_at?: string
    html_url?: string
  }
  agent: UpdateAgentStatus
  update?: UpdatePreparationStatus
}

export type CheckinWindow = {
  main_start: string
  main_end: string
  fallback_start: string
  fallback_end: string
}

export type SystemSettings = {
  cross_provider_model_pool: boolean
  checkin_disabled_accounts: boolean
  proxy_url: string
  routing_strategy: 'round-robin' | 'weighted-round-robin' | 'fill-first'
  rate_preference: boolean
  expiry_window_seconds: number
  secondary_expiry_window_seconds: number
  workbuddy_checkin_time: string
  checkin_times: Record<string, string>
  checkin_windows: Record<string, CheckinWindow>
  timezone: string
  session_affinity?: {
    ttl_seconds?: number
    capacity?: number
    bindings?: number
    hits?: number
    misses?: number
    escapes?: number
    rebindings?: number
    last_escape_at?: string
    last_escape_reason?: string
    last_miss_reason?: string
  }
}

export type StartUpdateResult = {
  job_id: string
  current_version?: string
  target_version?: string
  backup?: {
    name: string
    created_at: string
  }
}

export function fetchSystemUpdate(force = false, signal?: AbortSignal) {
  return api<SystemUpdateInfo>(`/api/system/update${force ? '?force=1' : ''}`, { signal })
}

export function fetchSystemSettings(signal?: AbortSignal) {
  return api<SystemSettings>('/api/system/settings', { signal })
}

export function updateSystemSettings(input: { cross_provider_model_pool?: boolean; checkin_disabled_accounts?: boolean; routing_strategy?: SystemSettings['routing_strategy']; rate_preference?: boolean; expiry_window_seconds?: number; secondary_expiry_window_seconds?: number; proxy_url?: string; workbuddy_checkin_time?: string; checkin_times?: Record<string, string>; checkin_windows?: Record<string, CheckinWindow> }, signal?: AbortSignal) {
  return api<SystemSettings>('/api/system/settings', {
    method: 'PATCH',
    body: JSON.stringify(input),
    signal,
  })
}

export function startSystemUpdate(signal?: AbortSignal) {
  return api<StartUpdateResult>('/api/system/update/prepare', { method: 'POST', body: '{}', signal })
}

export function applyPreparedSystemUpdate(signal?: AbortSignal) {
  return api<StartUpdateResult>('/api/system/update/apply', { method: 'POST', body: '{}', signal })
}

export function cancelSystemUpdate(signal?: AbortSignal) {
  return api<{ ok: boolean }>('/api/system/update/cancel', { method: 'POST', body: '{}', signal })
}

export function rollbackSystemUpdate(version: string, signal?: AbortSignal) {
  return api<StartUpdateResult>('/api/system/update/rollback', { method: 'POST', body: JSON.stringify({ version }), signal })
}

export type SystemResources = {
  sampled_at: string
  server: {
    pid: number
    rss_bytes: number
    heap_bytes?: number
    goroutines?: number
    cpu_percent: number
  }
  total_rss_bytes: number
}

export function fetchSystemResources(signal?: AbortSignal) {
  return api<SystemResources>('/api/system/resources', { signal })
}
