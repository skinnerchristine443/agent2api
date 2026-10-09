import { api } from './client'
import type { CheckinRecord, Overview } from './types'

export function fetchOverviewSummary(keyOverride?: string, signal?: AbortSignal) {
  return api<Overview>('/api/overview/summary', { signal }, keyOverride)
}

export function fetchAccounts(refresh = false, signal?: AbortSignal) {
  return api<{ object?: string; data?: NonNullable<Overview['accounts']> }>(
    `/api/accounts?refresh=${refresh ? '1' : '0'}`,
    { signal },
  )
}

export function startDeviceLogin(accountId?: string, signal?: AbortSignal) {
  if (!accountId) throw new Error('account id required')
  return api<{ authUrl?: string; status?: string; message?: string }>(`/api/accounts/${encodeURIComponent(accountId)}/login/device`, {
    method: 'POST',
    body: '{}',
    signal,
  })
}

export function fetchLoginStatus(accountId?: string, signal?: AbortSignal) {
  if (!accountId) throw new Error('account id required')
  return api<{ login?: any }>(`/api/accounts/${encodeURIComponent(accountId)}/login/status`, { signal })
}

export function completeLoginCallback(accountId: string, callbackUrl: string, signal?: AbortSignal) {
  if (!accountId) throw new Error('account id required')
  return api(`/api/accounts/${encodeURIComponent(accountId)}/login/callback`, {
    method: 'POST',
    body: JSON.stringify({ callback_url: callbackUrl }),
    signal,
  })
}

export function loginWithPat(pat: string, accountId?: string, signal?: AbortSignal) {
  if (!accountId) throw new Error('account id required')
  return api(`/api/accounts/${encodeURIComponent(accountId)}/login/pat`, {
    method: 'POST',
    body: JSON.stringify({ pat }),
    signal,
  })
}

type ModelsResponse = { data?: Overview['models'] }

type ModelsMemoryEntry = {
  data: ModelsResponse
  at: number
}

const modelsMemoryTTL = 30_000
const modelsMemoryCache = new Map<string, ModelsMemoryEntry>()

function modelsMemoryKey(accountId?: string, view?: 'regional') {
  return `${accountId || '*'}@${view || 'merged'}`
}

export function fetchModels(accountId?: string, refresh = false, view?: 'regional', signal?: AbortSignal) {
  const q = new URLSearchParams()
  if (refresh) q.set('refresh', '1')
  if (accountId) q.set('account', accountId)
  if (view) q.set('view', view)
  const query = q.toString()
  return api<ModelsResponse>(`/api/models${query ? `?${query}` : ''}`, { signal })
}

// 结果缓存（TTL 30s）：命中即复用，未命中即发起新请求。
// 不缓存「在途 promise」——调用方（useModelsCatalog）经 useApiQuery 取数，
// 并发去重与 abort 生命周期由 useApiQuery 的 inFlight 表负责；此处再复用
// 可能被 abort 的 pending 会在 StrictMode 双挂载（卸载即 abort → 复活读出
// 已中止的 promise）时返回永不落地的结果（历史缺陷，批次 4b 渲染验证发现）。
export function fetchModelsCached(accountId?: string, view?: 'regional', signal?: AbortSignal) {
  const key = modelsMemoryKey(accountId, view)
  const cached = modelsMemoryCache.get(key)
  if (cached && Date.now() - cached.at < modelsMemoryTTL) {
    return Promise.resolve(cached.data)
  }
  return fetchModels(accountId, false, view, signal).then((data) => {
    modelsMemoryCache.set(key, { data, at: Date.now() })
    return data
  })
}

export function refreshModels(accountId?: string, view?: 'regional', signal?: AbortSignal) {
  const key = modelsMemoryKey(accountId, view)
  modelsMemoryCache.delete(key)
  return fetchModels(accountId, true, view, signal).then((data) => {
    modelsMemoryCache.set(key, { data, at: Date.now() })
    return data
  })
}

export function updateModelContext(modelKey: string, contextLength: number, signal?: AbortSignal) {
  return api<{
    model: string
    context_length: number
    default_context_length: number
    context_custom: boolean
  }>(`/api/models/${encodeURIComponent(modelKey)}`, {
    method: 'PATCH',
    body: JSON.stringify({ context_length: contextLength }),
    signal,
  })
}

export function updateProviderMaxMode(provider: string, modelKey: string, maxMode: boolean, signal?: AbortSignal) {
  return api<{
    model: string
    provider: string
    max_mode: boolean
    reasoning_effort?: string
    context_custom: boolean
    context_length?: number
    default_context_length?: number
  }>(`/api/models/${encodeURIComponent(provider)}/${encodeURIComponent(modelKey)}`, {
    method: 'PATCH',
    body: JSON.stringify({ max_mode: maxMode }),
    signal,
  })
}

export function updateProviderReasoning(provider: 'trae' | 'workbuddy', modelKey: string, reasoningEffort: string, signal?: AbortSignal) {
  return api<{
    model: string
    provider: string
    max_mode: boolean
    reasoning_effort?: string
    context_custom: boolean
  }>(`/api/models/${provider}/${encodeURIComponent(modelKey)}`, {
    method: 'PATCH',
    body: JSON.stringify({ reasoning_effort: reasoningEffort }),
    signal,
  })
}

export function refreshAccount(accountId: string, options?: { quota?: boolean }, signal?: AbortSignal) {
  if (!accountId) throw new Error('account id required')
  const query = options?.quota ? '?quota=1' : ''
  return api(`/api/accounts/${encodeURIComponent(accountId)}/refresh${query}`, {
    method: 'POST',
    body: '{}',
    signal,
  })
}

export function testChat(model: string, content: string, accountId?: string, signal?: AbortSignal) {
  const headers: Record<string, string> = {}
  if (accountId) headers['X-Agent2API-Account'] = accountId
  return api('/api/chat', {
    method: 'POST',
    headers,
    body: JSON.stringify({
      model,
      stream: false,
      messages: [{ role: 'user', content }],
    }),
    signal,
  })
}


export type ProviderDescriptor = {
  id: string
  label: string
  runtime: string
  capabilities: {
    browser_login: boolean
    pat_login: boolean
    import_export: boolean
    /** 成长中心接线（成长页选择器据此过滤；老服务端可能缺省）。 */
    growth?: boolean
  }
  regions: Array<{ id: string; label: string; checkin?: { timezone: string } }>
  default_region: string
}

export function fetchProviders(signal?: AbortSignal) {
  return api<{ data?: ProviderDescriptor[] }>('/api/providers', { signal })
}

export function createAccount(
  name: string,
  provider = 'workbuddy',
  region = 'global',
  options?: {
    max_inflight?: number
    priority?: number
    drop_system_prompt?: boolean
    workbuddy_auto_checkin?: boolean
    workbuddy_checkin_time?: string
    auto_checkin?: boolean
    checkin_time?: string
    proxy_url?: string
  },
  signal?: AbortSignal,
) {
  return api('/api/accounts', {
    method: 'POST',
    body: JSON.stringify({
      name,
      provider,
      region,
      enabled: true,
      max_inflight: options?.max_inflight ?? 4,
      priority: options?.priority ?? 50,
      drop_system_prompt: options?.drop_system_prompt,
      workbuddy_auto_checkin: options?.workbuddy_auto_checkin,
      workbuddy_checkin_time: options?.workbuddy_checkin_time,
      auto_checkin: options?.auto_checkin,
      checkin_time: options?.checkin_time,
      proxy_url: options?.proxy_url,
    }),
    signal,
  })
}

export function clearAccountCooldowns(accountId: string, model?: string, signal?: AbortSignal) {
  return api<{ cleared?: number }>(`/api/accounts/${encodeURIComponent(accountId)}/cooldowns/clear`, {
    method: 'POST',
    body: JSON.stringify(model ? { model } : {}),
    signal,
  })
}

export function checkinAccount(accountId: string, signal?: AbortSignal) {
  return api(`/api/accounts/${encodeURIComponent(accountId)}/checkin`, {
    method: 'POST',
    body: '{}',
    signal,
  })
}

export function fetchCheckinRecords(accountId: string, signal?: AbortSignal) {
  return api<{ object?: string; data?: CheckinRecord[] }>(`/api/accounts/${encodeURIComponent(accountId)}/checkins`, { signal })
}

export function updateAccount(accountId: string, input: Record<string, unknown>, signal?: AbortSignal) {
  return api(`/api/accounts/${encodeURIComponent(accountId)}`, { method: 'PATCH', body: JSON.stringify(input), signal })
}

export function deleteAccount(accountId: string, signal?: AbortSignal) {
  return api(`/api/accounts/${encodeURIComponent(accountId)}`, { method: 'DELETE', signal })
}

export function importAccount(bundle: Record<string, unknown>, signal?: AbortSignal) {
  return api('/api/accounts/import', { method: 'POST', body: JSON.stringify(bundle), signal })
}

export type ImportBatchItem = {
  index: number
  status: string
  account_id?: string
  name?: string
  error?: string
}

export type ImportBatchResponse = {
  results?: ImportBatchItem[]
  imported?: number
  skipped?: number
  errors?: number
}

export function importAccountsBatch(items: unknown[], signal?: AbortSignal) {
  return api<ImportBatchResponse>('/api/accounts/import/batch', {
    method: 'POST',
    body: JSON.stringify({ items }),
    signal,
  })
}

export type QuotaAlert = {
  account_id: string
  account_name?: string
  category: string
  message: string
  remaining?: number
  unit?: string
}

export function fetchQuotaAlerts(signal?: AbortSignal) {
  return api<{ data?: QuotaAlert[]; count?: number }>('/api/alerts', { signal })
}

export function exportAccount(accountId: string, signal?: AbortSignal) {
  return api<Record<string, unknown>>(`/api/accounts/${encodeURIComponent(accountId)}/export`, { signal })
}
