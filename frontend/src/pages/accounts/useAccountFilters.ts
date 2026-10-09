import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

/** 文本搜索防抖（与全局约定一致：280ms）后再写 URL。 */
const QUERY_DEBOUNCE_MS = 280

export const ACCOUNT_STATE_FILTERS = ['all', 'available', 'attention', 'disabled'] as const
export type AccountStateFilter = (typeof ACCOUNT_STATE_FILTERS)[number]

/** 快捷筛选条分类（设计 D13：可用 / 需关注 / 在途（冷却））。 */
export const ACCOUNT_QUICK_FILTERS = ['all', 'avail', 'attn', 'transit'] as const
export type AccountQuickFilter = (typeof ACCOUNT_QUICK_FILTERS)[number]

/** 排序模式（设计 D12：默认稳定序；其余为显式动作）。 */
export const ACCOUNT_SORT_MODES = ['priority', 'quota', 'fresh', 'severity'] as const
export type AccountSortMode = (typeof ACCOUNT_SORT_MODES)[number]

export type AccountFilters = {
  /** 已落 URL 的搜索词（防抖后）。输入框请用 `draftQuery`。 */
  query: string
  provider: string
  state: AccountStateFilter
  /** 快捷筛选条分类（摘要条 4 格）。 */
  quick: AccountQuickFilter
  /** 组内排序（默认 priority = 稳定序「优先级 → 名称」）。 */
  sort: AccountSortMode
  /** 收件箱跳转目标（`?focus=`；只读——由跳转方写入，本 hook 不改写）。 */
  focus: string
  /** 输入框当前值（未防抖）。 */
  draftQuery: string
  setQuery: (value: string) => void
  setProvider: (value: string) => void
  setState: (value: AccountStateFilter) => void
  setQuick: (value: AccountQuickFilter) => void
  setSort: (value: AccountSortMode) => void
  clear: () => void
  /** 稳定键：交给依赖筛选的查询，变化即回落第 1 页。 */
  filtersKey: string
}

function isStateFilter(value: string | null): value is AccountStateFilter {
  return value !== null && (ACCOUNT_STATE_FILTERS as readonly string[]).includes(value)
}

function isQuickFilter(value: string | null): value is AccountQuickFilter {
  return value !== null && (ACCOUNT_QUICK_FILTERS as readonly string[]).includes(value)
}

function isSortMode(value: string | null): value is AccountSortMode {
  return value !== null && (ACCOUNT_SORT_MODES as readonly string[]).includes(value)
}

/**
 * 账号池筛选（§7.3 URL 状态规范：`?q=` / `?provider=` / `?state=` / `?quick=` / `?sort=`）。
 *
 * - URL 是唯一事实源；默认值（空串 / all / priority）不写入 URL，保持链接干净；
 * - 写入一律 `replace`（不污染历史；页签 / 详情才用 push）；
 * - 文本搜索保留 280ms 防抖后再落 URL（现状语义）；
 * - 不在此处改 `page`：分页回落由依赖 filtersKey 的查询统一处理。
 */
export function useAccountFilters(): AccountFilters {
  const [searchParams, setSearchParams] = useSearchParams()
  const rawState = searchParams.get('state')
  const rawQuick = searchParams.get('quick')
  const rawSort = searchParams.get('sort')
  const query = searchParams.get('q') ?? ''
  const provider = searchParams.get('provider') ?? ''
  const focus = searchParams.get('focus') ?? ''
  const state: AccountStateFilter = isStateFilter(rawState) ? rawState : 'all'
  const quick: AccountQuickFilter = isQuickFilter(rawQuick) ? rawQuick : 'all'
  const sort: AccountSortMode = isSortMode(rawSort) ? rawSort : 'priority'
  const [draftQuery, setDraftQuery] = useState(query)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // 外部变化（前进 / 后退 / 直链）时同步输入框
  useEffect(() => {
    setDraftQuery(query)
  }, [query])

  useEffect(() => () => {
    if (timerRef.current !== null) clearTimeout(timerRef.current)
  }, [])

  const write = useCallback((patch: Record<string, string>) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      for (const [key, value] of Object.entries(patch)) {
        if (value) params.set(key, value)
        else params.delete(key)
      }
      return params
    }, { replace: true })
  }, [setSearchParams])

  const setQuery = useCallback((value: string) => {
    setDraftQuery(value)
    if (timerRef.current !== null) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      timerRef.current = null
      write({ q: value.trim() })
    }, QUERY_DEBOUNCE_MS)
  }, [write])

  const setProvider = useCallback((value: string) => {
    write({ provider: value.trim().toLowerCase() })
  }, [write])

  const setStateFilter = useCallback((value: AccountStateFilter) => {
    write({ state: value === 'all' ? '' : value })
  }, [write])

  const setQuickFilter = useCallback((value: AccountQuickFilter) => {
    write({ quick: value === 'all' ? '' : value })
  }, [write])

  const setSortMode = useCallback((value: AccountSortMode) => {
    write({ sort: value === 'priority' ? '' : value })
  }, [write])

  const clear = useCallback(() => {
    setDraftQuery('')
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current)
      timerRef.current = null
    }
    write({ q: '', provider: '', state: '', quick: '', sort: '' })
  }, [write])

  return {
    query,
    provider,
    state,
    quick,
    sort,
    focus,
    draftQuery,
    setQuery,
    setProvider,
    setState: setStateFilter,
    setQuick: setQuickFilter,
    setSort: setSortMode,
    clear,
    filtersKey: [query, provider, state, quick, sort].join('\u0000'),
  }
}
