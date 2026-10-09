import { getLocalTimeZone, parseAbsoluteToLocal, toCalendarDateTime, type DateValue } from '@internationalized/date'

import type { RequestLog } from '@/api/logs'

// 日志域的**纯函数与取值域**（无 JSX、无取数、无 React）：格式化 / 配色 / 时间
// 预设 / URL 筛选参数的读写与稳定键。请求页与运行页、数据层（hooks）共用一份，
// 避免多处实现漂移。
//
// 分工（方案 §7.1 规则 1 的扫描白名单「数据层与纯函数区」）：取数在
// `@/hooks/useLogsQueries`，URL ↔ React 状态在 `@/pages/logs/useLogsFilters`，
// 本文件只放两边都要用的纯逻辑。

/** 请求状态筛选（URL `status`；默认 `all` 不写入 URL）。 */
export type RequestFilter = 'all' | 'ok' | 'incomplete' | 'error' | 'canceled'
/** 运行日志级别筛选（URL `level`；默认 `all` 不写入 URL）。 */
export type RuntimeFilter = 'all' | 'info' | 'warn' | 'error'
/** 流式筛选（URL `stream`；默认 `all` 不写入 URL）。 */
export type StreamFilter = 'all' | 'stream' | 'sync'
/** 时间预设（URL `range`；默认 `1h` 不写入 URL，`custom` 配合 `from`/`to`）。 */
export type TimeRange = 'all' | '1h' | '24h' | '7d' | 'custom'
/** 错误类型筛选（URL `kind` ↔ API `error_kind`；默认 `all` 不写入 URL）。 */
export type ErrorKindFilter =
  | 'all'
  | 'quota'
  | 'rate_limit'
  | 'auth'
  | 'not_ready'
  | 'unavailable'
  | 'invalid_request'
  | 'model_not_available'

export type DateRangeValue = { start: DateValue; end: DateValue }

/** URL 取值域（非法取值一律回落默认，防手改 URL 把 UI 打进非法态）。 */
export const REQUEST_STATUS_VALUES = ['ok', 'incomplete', 'error', 'canceled'] as const
export const ERROR_KIND_VALUES = [
  'quota',
  'rate_limit',
  'auth',
  'not_ready',
  'unavailable',
  'invalid_request',
  'model_not_available',
] as const
export const STREAM_VALUES = ['stream', 'sync'] as const
export const TIME_RANGE_VALUES = ['all', '1h', '24h', '7d', 'custom'] as const
export const RUNTIME_LEVEL_VALUES = ['info', 'warn', 'error'] as const

/** 从 URL 读枚举：非法 / 缺失返回 `undefined`（调用方补默认值）。 */
export function pickEnum<const T extends readonly string[]>(
  raw: string | null | undefined,
  allowed: T,
): T[number] | undefined {
  if (!raw) return undefined
  return (allowed as readonly string[]).includes(raw) ? (raw as T[number]) : undefined
}

export function statusColor(status?: string): 'success' | 'warning' | 'danger' | 'default' {
  if (status === 'ok') return 'success'
  if (status === 'streaming' || status === 'started' || status === 'incomplete') return 'warning'
  if (status === 'error' || status === 'canceled') return 'danger'
  return 'default'
}

export function levelDot(level?: string) {
  if (level === 'error') return 'danger'
  if (level === 'warn') return undefined
  return 'ok'
}

export function runtimeLevelColor(level?: string): 'success' | 'warning' | 'danger' | 'default' {
  if (level === 'error') return 'danger'
  if (level === 'warn') return 'warning'
  if (level === 'info') return 'success'
  return 'default'
}

export function formatTime(value?: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

export function reasoningLabel(log: Pick<RequestLog, 'requested_reasoning' | 'resolved_reasoning'>) {
  const requested = log.requested_reasoning?.trim() || ''
  const resolved = log.resolved_reasoning?.trim() || ''
  if (!requested && !resolved) return ''
  if (requested && resolved && requested !== resolved) return `${requested} → ${resolved}`
  return resolved || requested
}

export function formatLatency(ms?: number | null) {
  if (ms == null) return '—'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

export function formatCredit(value?: number | null) {
  if (value == null || !Number.isFinite(value)) return '—'
  return String(Math.round(value * 10000) / 10000)
}

/** DateValue → ISO（`endOfMinute` 用于自定义区间的结束时刻：取整分末）。 */
export function dateValueToISO(value: DateValue | null | undefined, endOfMinute = false) {
  if (!value) return undefined
  const date = value.toDate(getLocalTimeZone())
  if (!Number.isFinite(date.getTime())) return undefined
  if (endOfMinute) date.setSeconds(59, 999)
  return date.toISOString()
}

/** ISO → `DateValue`（截到分钟，与 `granularity="minute"` 的选择器对齐；非法值返回 null）。 */
export function isoToDateValue(value?: string | null): DateValue | null {
  if (!value) return null
  const parsed = new Date(value)
  if (!Number.isFinite(parsed.getTime())) return null
  try {
    return toCalendarDateTime(parseAbsoluteToLocal(parsed.toISOString())).set({ second: 0, millisecond: 0 })
  } catch {
    return null
  }
}

/** URL 的 `from`/`to` → 选择器值；两者缺一即视为未选择（与现状一致）。 */
export function customRangeFromISO(from?: string | null, to?: string | null): DateRangeValue | null {
  const start = isoToDateValue(from)
  const end = isoToDateValue(to)
  if (!start || !end) return null
  return { start, end }
}

/** 时间预设 → 相对「此刻」的查询区间（`all` / `custom` 由调用方另行处理）。 */
export function rangeFromPreset(preset: TimeRange) {
  if (preset === 'all' || preset === 'custom') {
    return { from: undefined as string | undefined, to: undefined as string | undefined }
  }
  const now = new Date()
  const from = new Date(now)
  if (preset === '1h') from.setHours(from.getHours() - 1)
  if (preset === '24h') from.setHours(from.getHours() - 24)
  if (preset === '7d') from.setDate(from.getDate() - 7)
  return { from: from.toISOString(), to: now.toISOString() }
}

// ── 请求日志 URL 筛选（§7.3） ─────────────────────────────────────────────

export type RequestLogsFilters = {
  id: string
  q: string
  status: RequestFilter
  kind: ErrorKindFilter
  model: string
  account: string
  stream: StreamFilter
  range: TimeRange
  from: string
  to: string
}

export const REQUEST_LOGS_FILTER_DEFAULTS: RequestLogsFilters = {
  id: '',
  q: '',
  status: 'all',
  kind: 'all',
  model: '',
  account: '',
  stream: 'all',
  range: '1h',
  from: '',
  to: '',
}

export function readRequestLogsFilters(params: URLSearchParams): RequestLogsFilters {
  return {
    id: params.get('id') ?? '',
    q: params.get('q') ?? '',
    status: pickEnum(params.get('status'), REQUEST_STATUS_VALUES) ?? 'all',
    kind: pickEnum(params.get('kind'), ERROR_KIND_VALUES) ?? 'all',
    model: params.get('model') ?? '',
    account: params.get('account') ?? '',
    stream: pickEnum(params.get('stream'), STREAM_VALUES) ?? 'all',
    range: pickEnum(params.get('range'), TIME_RANGE_VALUES) ?? '1h',
    from: params.get('from') ?? '',
    to: params.get('to') ?? '',
  }
}

function writeFilterParam(params: URLSearchParams, key: string, value: string, defaultValue: string) {
  if (value && value !== defaultValue) params.set(key, value)
  else params.delete(key)
}

export function writeRequestLogsFilters(params: URLSearchParams, filters: RequestLogsFilters) {
  writeFilterParam(params, 'id', filters.id, '')
  writeFilterParam(params, 'q', filters.q, '')
  writeFilterParam(params, 'status', filters.status, 'all')
  writeFilterParam(params, 'kind', filters.kind, 'all')
  writeFilterParam(params, 'model', filters.model, '')
  writeFilterParam(params, 'account', filters.account, '')
  writeFilterParam(params, 'stream', filters.stream, 'all')
  writeFilterParam(params, 'range', filters.range, '1h')
  const custom = filters.range === 'custom'
  writeFilterParam(params, 'from', custom ? filters.from : '', '')
  writeFilterParam(params, 'to', custom ? filters.to : '', '')
}

export function requestLogsHasFilters(filters: RequestLogsFilters) {
  return Boolean(
    filters.id
    || filters.q
    || filters.status !== 'all'
    || filters.kind !== 'all'
    || filters.model
    || filters.account
    || filters.stream !== 'all'
    || filters.range !== '1h',
  )
}

/** 筛选稳定键：进 `usePagedQuery` 当 `filtersKey`（变化即回第 1 页），并作 depsKey 的一半。 */
export function requestLogsFiltersKey(filters: RequestLogsFilters) {
  const custom = filters.range === 'custom'
  return [
    filters.id,
    filters.q,
    filters.status,
    filters.kind,
    filters.model,
    filters.account,
    filters.stream,
    filters.range,
    custom ? filters.from : '',
    custom ? filters.to : '',
  ].join('\u0000')
}

// ── 运行日志 URL 筛选（§7.3；`level` 为本批登记的参数名） ───────────────────

export type RuntimeLogsFilters = {
  q: string
  account: string
  level: RuntimeFilter
}

export const RUNTIME_LOGS_FILTER_DEFAULTS: RuntimeLogsFilters = { q: '', account: '', level: 'all' }

export function readRuntimeLogsFilters(params: URLSearchParams): RuntimeLogsFilters {
  return {
    q: params.get('q') ?? '',
    account: params.get('account') ?? '',
    level: pickEnum(params.get('level'), RUNTIME_LEVEL_VALUES) ?? 'all',
  }
}

export function writeRuntimeLogsFilters(params: URLSearchParams, filters: RuntimeLogsFilters) {
  writeFilterParam(params, 'q', filters.q, '')
  writeFilterParam(params, 'account', filters.account, '')
  writeFilterParam(params, 'level', filters.level, 'all')
}

export function runtimeLogsHasFilters(filters: RuntimeLogsFilters) {
  return Boolean(filters.q || filters.account || filters.level !== 'all')
}

export function runtimeLogsFiltersKey(filters: RuntimeLogsFilters) {
  return [filters.q, filters.account, filters.level].join('\u0000')
}
