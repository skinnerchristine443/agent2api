import { useCallback, useEffect, useMemo, useRef } from 'react'

import {
  clearRequestLogs,
  fetchRequestLog,
  fetchRequestLogs,
  fetchRuntimeLogs,
  type RequestLogQuery,
} from '@/api/logs'
import { fetchAccounts, fetchModels } from '@/api/overview'
import type { Overview } from '@/api/types'
import type { FilterSelectOption } from '@/components/ui/FilterSelect'
import { useApiQuery } from '@/hooks/useApiQuery'
import { useI18n } from '@/hooks/I18nContext'
import { usePagedQuery } from '@/hooks/usePagedQuery'
import { accountNameMap } from '@/lib/account'
import { accountProviderLabel } from '@/lib/provider'
import {
  rangeFromPreset,
  requestLogsFiltersKey,
  runtimeLogsFiltersKey,
  type RequestLogsFilters,
  type RuntimeLogsFilters,
} from '@/lib/logsFormat'

// 日志域的**数据接入层**（方案 §7.1 规则 1 的扫描白名单「数据层」）：URL 筛选态
// （`@/pages/logs/useLogsFilters`）↔ 查询身份（depsKey）↔ api 层取数。页面只消费
// 本模块，不直接接触 `@/api`。

/** 日志列表默认每页条数（与迁移前一致）。 */
export const LOGS_PAGE_SIZE = 50
/** 运行日志自动刷新间隔（仅第 1 页；§4.4 ⑧）。 */
export const RUNTIME_LOGS_POLL_MS = 3000

const ALLOWED_PAGE_SIZES = [20, 50, 100]

/** URL `size` 容许值收口（手改 URL 传入清单外页长时回退默认，防分页控件进非法态）。 */
export function sanitizePageSize(size: number) {
  return ALLOWED_PAGE_SIZES.includes(size) ? size : LOGS_PAGE_SIZE
}

export function buildRequestLogsQuery(filters: RequestLogsFilters, page: number, size: number): RequestLogQuery {
  // 精确 ID 查询忽略时间窗（迁移前语义：给定请求 ID 时不再叠时间下限）。
  const timeRange = filters.id
    ? { from: undefined, to: undefined }
    : filters.range === 'custom'
      ? { from: filters.from || undefined, to: filters.to || undefined }
      : rangeFromPreset(filters.range)
  return {
    status: filters.status === 'all' ? undefined : filters.status,
    id: filters.id || undefined,
    q: filters.q || undefined,
    account: filters.account || undefined,
    model: filters.model || undefined,
    stream: filters.stream === 'all' ? undefined : filters.stream === 'stream',
    error_kind: filters.kind === 'all' ? undefined : filters.kind,
    from: timeRange.from,
    to: timeRange.to,
    limit: size,
    offset: (page - 1) * size,
  }
}

export function requestLogsDepsKey(filters: RequestLogsFilters, page: number, size: number) {
  return `logs:requests\u0000${requestLogsFiltersKey(filters)}\u0000${page}\u0000${size}`
}

export function buildRuntimeLogsQuery(filters: RuntimeLogsFilters, page: number, size: number) {
  return {
    level: filters.level === 'all' ? undefined : filters.level,
    q: filters.q.trim() || undefined,
    account: filters.account || undefined,
    limit: size,
    offset: (page - 1) * size,
  }
}

export function runtimeLogsDepsKey(filters: RuntimeLogsFilters, page: number, size: number) {
  return `logs:runtime\u0000${runtimeLogsFiltersKey(filters)}\u0000${page}\u0000${size}`
}

/**
 * 请求日志列表：URL 分页 + 筛选变化重置页码 + 越界页码回收；**无轮询**（手动刷新）。
 */
export function useRequestLogsQuery(filters: RequestLogsFilters) {
  const filtersKey = requestLogsFiltersKey(filters)
  const { page, size: rawSize, setPage, setSize } = usePagedQuery({ defaultSize: LOGS_PAGE_SIZE, filtersKey })
  const size = sanitizePageSize(rawSize)
  const effectivePage = useFilterResetPage(filtersKey, page)

  const query = useApiQuery(
    (signal) => fetchRequestLogs(buildRequestLogsQuery(filters, effectivePage, size), signal),
    requestLogsDepsKey(filters, effectivePage, size),
  )

  const items = query.data?.items ?? []
  const total = query.data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / size))

  // 越界页码回收（等价迁移前 render 期收敛）：数据回来后页码超出总页数即回退末页。
  useEffect(() => {
    if (query.loading || effectivePage <= pageCount) return
    setPage(pageCount)
  }, [effectivePage, pageCount, query.loading, setPage])

  return { query, items, total, page: effectivePage, size, pageCount, setPage, setSize }
}

/**
 * 运行日志列表：URL 分页 + 筛选变化重置页码；**仅第 1 页 3s 轮询**
 * （翻页 / 切走即停，回第 1 页恢复——条件轮询交给 `useApiQuery` 的 `pollMs`）。
 */
export function useRuntimeLogsQuery(filters: RuntimeLogsFilters) {
  const filtersKey = runtimeLogsFiltersKey(filters)
  const { page, size: rawSize, setPage, setSize } = usePagedQuery({ defaultSize: LOGS_PAGE_SIZE, filtersKey })
  const size = sanitizePageSize(rawSize)
  const effectivePage = useFilterResetPage(filtersKey, page)

  const query = useApiQuery(
    (signal) => fetchRuntimeLogs(buildRuntimeLogsQuery(filters, effectivePage, size), signal),
    runtimeLogsDepsKey(filters, effectivePage, size),
    { pollMs: effectivePage === 1 ? RUNTIME_LOGS_POLL_MS : null },
  )

  const items = query.data?.items ?? []
  const total = query.data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / size))

  useEffect(() => {
    if (query.loading || effectivePage <= pageCount) return
    setPage(pageCount)
  }, [effectivePage, pageCount, query.loading, setPage])

  return { query, items, total, page: effectivePage, size, pageCount, setPage, setSize }
}

/**
 * 筛选变化时**当帧**即按第 1 页取数（等价迁移前的 render 期页码收敛）：
 * 否则会先发一次「旧页码 + 新筛选」的请求，再被 `usePagedQuery` 的重置
 * 拉回第 1 页重发一次——同一操作两倍上游请求。URL 的 `page=` 仍由
 * `usePagedQuery` 的 filtersKey 重置负责删除，两者触发条件同源。
 *
 * 收敛条件用「URL 已经落回第 1 页」而不是「本次渲染」：重置写入与取数
 * 在途状态会各自触发一轮渲染，按渲染收敛会出现 page 1 → 2 → 1 的来回。
 */
function useFilterResetPage(filtersKey: string, page: number) {
  const appliedRef = useRef(filtersKey)
  const pending = filtersKey !== appliedRef.current // eslint-disable-line react/refs -- 渲染期读 ref 推导页码收敛
  useEffect(() => {
    if (!pending || page !== 1) return
    appliedRef.current = filtersKey
  }, [filtersKey, page, pending])
  return pending ? 1 : page
}

// ── 筛选选项（账号 / 模型）与详情 / 写操作 ───────────────────────────────

type LogsFilterOptionsData = {
  accounts: NonNullable<Overview['accounts']>
  models: NonNullable<Overview['models']>
}

/** 账号 + 模型选项：任一接口失败只丢该项（迁移前 allSettled 语义），不拖垮页面。 */
async function fetchLogsFilterOptions(signal: AbortSignal): Promise<LogsFilterOptionsData> {
  const [accountsResult, modelsResult] = await Promise.allSettled([
    fetchAccounts(false, signal),
    fetchModels(undefined, false, undefined, signal),
  ])
  return {
    accounts: accountsResult.status === 'fulfilled' ? accountsResult.value.data ?? [] : [],
    models: modelsResult.status === 'fulfilled' ? modelsResult.value.data ?? [] : [],
  }
}

export type LogsFilterOptions = {
  accountOptions: FilterSelectOption[]
  modelOptions: FilterSelectOption[]
  accountNameById: Map<string, string>
  providerLabel: (item: { account_id?: string; provider?: string }) => string
}

/** 账号名 / 渠道标签 / 下拉选项（两个日志页共用）。 */
export function useLogsFilterOptions(): LogsFilterOptions {
  const { t } = useI18n()
  const query = useApiQuery(fetchLogsFilterOptions, 'logs:filter-options')
  // `?? []` 每次渲染都新建数组 ⇒ 先经 useMemo 落成稳定引用，再进下游 useMemo 依赖。
  const accounts = useMemo(() => query.data?.accounts ?? [], [query.data])
  const models = useMemo(() => query.data?.models ?? [], [query.data])

  const accountNameById = useMemo(() => accountNameMap(accounts), [accounts])

  const accountProviderById = useMemo(() => {
    const providers = new Map<string, { provider?: string; region?: string }>()
    for (const account of accounts) {
      providers.set(account.id, { provider: account.provider, region: account.region })
    }
    return providers
  }, [accounts])

  const accountOptions = useMemo(
    () => accounts.map((account) => ({ id: account.id, label: account.name || account.id })),
    [accounts],
  )

  const modelOptions = useMemo(() => {
    const seen = new Map<string, FilterSelectOption>()
    for (const model of models) {
      if (!seen.has(model.id)) seen.set(model.id, { id: model.id, label: model.display_name || model.id })
    }
    return [...seen.values()]
  }, [models])

  const providerLabel = useCallback((item: { account_id?: string; provider?: string }) => {
    const account = item.account_id ? accountProviderById.get(item.account_id) : undefined
    const provider = item.provider || account?.provider
    if (!provider) return '—'
    return accountProviderLabel(provider, account?.region, t)
  }, [accountProviderById, t])

  return { accountOptions, modelOptions, accountNameById, providerLabel }
}

/** 请求详情取数（`?request=<id>` 直达只取一次）。 */
export function fetchRequestLogDetail(id: string, signal?: AbortSignal) {
  return fetchRequestLog(id, signal)
}

/** 请求 ID 搜索框的候选（按 ID 模糊匹配，20 条上限）。 */
export function loadRequestIdOptions(query: string): Promise<FilterSelectOption[]> {
  return fetchRequestLogs({ q: query || undefined, limit: 20, offset: 0 }).then((result) =>
    (result.items || []).map((item) => ({ id: item.id, label: item.id })),
  )
}

/** 清空请求历史（写操作，经 `useAsyncAction` 调用）。 */
export function clearLogHistory() {
  return clearRequestLogs()
}
