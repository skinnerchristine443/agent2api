import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import {
  RUNTIME_LOGS_FILTER_DEFAULTS,
  REQUEST_LOGS_FILTER_DEFAULTS,
  readRequestLogsFilters,
  readRuntimeLogsFilters,
  requestLogsHasFilters,
  runtimeLogsHasFilters,
  writeRequestLogsFilters,
  writeRuntimeLogsFilters,
  type RequestLogsFilters,
  type RuntimeLogsFilters,
} from '@/lib/logsFormat'

// 日志域的 URL 筛选态（React 侧）：把 §7.3 的参数协议接到 `useSearchParams`。
// 纯逻辑（读写 / 稳定键 / 取值域）在 `@/lib/logsFormat`；取数在
// `@/hooks/useLogsQueries`。
//
// 写入语义：筛选一律 `replace`（不污染历史）；**默认值不写 URL**（保持链接干净）；
// 文本搜索 280ms 防抖后写 URL（计时从输入当下重启）。

/** 文本搜索防抖（迁移前语义：280ms，计时从输入当下重启）。 */
export const LOGS_SEARCH_DEBOUNCE_MS = 280

/** 请求日志筛选态：URL 为单一事实源（读取 + 补丁写入 + 清空）。 */
export function useRequestLogsFilters() {
  const [searchParams, setSearchParams] = useSearchParams()
  const filters = useMemo(() => readRequestLogsFilters(searchParams), [searchParams])

  const patch = useCallback((next: Partial<RequestLogsFilters>) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      writeRequestLogsFilters(params, { ...readRequestLogsFilters(params), ...next })
      return params
    }, { replace: true })
  }, [setSearchParams])

  const clear = useCallback(() => {
    patch(REQUEST_LOGS_FILTER_DEFAULTS)
  }, [patch])

  return { filters, patch, clear, hasFilters: requestLogsHasFilters(filters) }
}

/**
 * 运行日志筛选态：搜索词走 280ms 防抖后写 URL，级别 / 账号立即写。
 *
 * `search` 是搜索框的受控态（防抖草稿），`patch` 供级别 / 账号 / 清空使用。
 */
export function useRuntimeLogsFilters() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { value: searchValue, setValue: setSearchValue } = useDebouncedSearchParam('q', LOGS_SEARCH_DEBOUNCE_MS)
  const filters = useMemo(() => readRuntimeLogsFilters(searchParams), [searchParams])

  const patch = useCallback((next: Partial<RuntimeLogsFilters>) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      writeRuntimeLogsFilters(params, { ...readRuntimeLogsFilters(params), ...next })
      return params
    }, { replace: true })
  }, [setSearchParams])

  const clear = useCallback(() => {
    setSearchValue('')
    patch(RUNTIME_LOGS_FILTER_DEFAULTS)
  }, [patch, setSearchValue])

  return {
    filters,
    patch,
    clear,
    hasFilters: runtimeLogsHasFilters(filters),
    search: { value: searchValue, setValue: setSearchValue },
  }
}

export type DebouncedSearchParam = {
  /** 输入框受控值（防抖草稿）。 */
  value: string
  setValue: (value: string) => void
}

/**
 * 文本搜索 → URL 参数的防抖写入（默认 280ms，迁移前语义）。
 *
 * - 计时从**输入当下**重启（连续输入只在停手 280ms 后写一次 URL）；
 * - URL 变化（直达 / 后退 / 清空筛选）反向同步草稿；
 * - 写入用 replace（不污染历史），默认值（空串）即删除参数。
 */
export function useDebouncedSearchParam(param: string, delayMs = LOGS_SEARCH_DEBOUNCE_MS): DebouncedSearchParam {
  const [searchParams, setSearchParams] = useSearchParams()
  const committed = searchParams.get(param) ?? ''
  const [draft, setDraft] = useState(committed)

  useEffect(() => {
    setDraft(committed) // eslint-disable-line react/set-state-in-effect -- 外部(URL)变化即同步草稿
  }, [committed])

  useEffect(() => {
    if (draft === committed) return
    const timer = setTimeout(() => {
      setSearchParams((prev) => {
        const params = new URLSearchParams(prev)
        if (draft) params.set(param, draft)
        else params.delete(param)
        return params
      }, { replace: true })
    }, delayMs)
    return () => clearTimeout(timer)
  }, [committed, delayMs, draft, param, setSearchParams])

  return { value: draft, setValue: setDraft }
}
