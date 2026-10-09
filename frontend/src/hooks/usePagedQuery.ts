import { useCallback, useEffect, useRef } from 'react'
import { useSearchParams } from 'react-router-dom'

/** 默认每页条数（与账号页现状一致；日志页等按需通过 defaultSize 覆盖）。 */
export const DEFAULT_PAGE_SIZE = 20

export type PagedQueryOptions = {
  /** 每页条数默认值：URL 无 size 时生效，且默认值不写入 URL。 */
  defaultSize?: number
  /**
   * 当前筛选条件的稳定键（如规范化后的查询串）。变化即把分页重置到第 1 页
   * ——§7.3「筛选变化随 URL 同步并重置分页」。
   */
  filtersKey?: string
}

export type PagedQueryResult = {
  page: number
  size: number
  setPage: (page: number) => void
  /** 换页长（并回到第 1 页，避免页码越界）。 */
  setSize: (size: number) => void
  resetToFirst: () => void
}

function parsePositiveInt(raw: string | null, fallback: number) {
  const value = Number.parseInt(raw ?? '', 10)
  return Number.isFinite(value) && value > 0 ? value : fallback
}

/**
 * 分页状态与 URL 同步（`?page=&size=`，§7.3）。
 *
 * - 默认值不写入 URL（page=1 / size=defaultSize 时删除参数，保持链接干净）；
 * - 筛选 / 分页写入一律 `replace`（不污染浏览器历史）；
 * - 首帧不改写 URL：`?page=3` 直链原样保留，只有 filtersKey 变化才回落第 1 页。
 */
export function usePagedQuery(opts: PagedQueryOptions = {}): PagedQueryResult {
  const { defaultSize = DEFAULT_PAGE_SIZE, filtersKey = '' } = opts
  const [searchParams, setSearchParams] = useSearchParams()

  const page = parsePositiveInt(searchParams.get('page'), 1)
  const size = parsePositiveInt(searchParams.get('size'), defaultSize)

  const write = useCallback((nextPage: number, nextSize: number) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (nextPage > 1) params.set('page', String(nextPage))
      else params.delete('page')
      if (nextSize > 0 && nextSize !== defaultSize) params.set('size', String(nextSize))
      else params.delete('size')
      return params
    }, { replace: true })
  }, [setSearchParams, defaultSize])

  const setPage = useCallback((nextPage: number) => {
    write(Math.max(1, Math.floor(nextPage)), size)
  }, [write, size])

  const setSize = useCallback((nextSize: number) => {
    write(1, Math.max(1, Math.floor(nextSize)))
  }, [write])

  const resetToFirst = useCallback(() => {
    write(1, size)
  }, [write, size])

  const filtersRef = useRef<string | null>(null)
  useEffect(() => {
    if (filtersRef.current === filtersKey) return
    const isFirstRun = filtersRef.current === null
    filtersRef.current = filtersKey
    if (isFirstRun) return // 直链首帧保持原页码，不改写 URL
    if (page !== 1) write(1, size)
  }, [filtersKey, page, size, write])

  return { page, size, setPage, setSize, resetToFirst }
}
