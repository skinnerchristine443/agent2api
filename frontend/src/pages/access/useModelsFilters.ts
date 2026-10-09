import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import { modelsFiltersKey } from './modelsFilter'

/** 文本筛选防抖（沿用现状 280ms；§7.3「文本输入类筛选保留防抖后再写 URL」）。 */
export const FILTER_DEBOUNCE_MS = 280

/**
 * 模型目录筛选的 URL 状态（§7.3）：
 *
 * - `q` 文本搜索：输入即时反馈（本地 state），防抖 280ms 后才写 URL；默认值
 *   （空串）不写入，链接保持干净；
 * - `provider` 渠道筛选：选择即写 URL；
 * - 一律 `replace`（筛选不污染浏览器历史）；外部导航（后退 / 直链）改 URL 时
 *   回灌输入框，但本 hook 自己的写入不回灌（避免吞掉输入中的空格）。
 *
 * `filtersKey` 供 usePagedQuery 使用：任一筛选变化即把分页重置到第 1 页。
 */
export function useModelsFilters() {
  const [searchParams, setSearchParams] = useSearchParams()
  const urlQ = searchParams.get('q') || ''
  const provider = searchParams.get('provider') || ''
  const [q, setQ] = useState(urlQ)
  // 最近一次由本 hook 写入 URL 的 q：URL 回灌时用它区分「外部变化」与「自己的写入」。
  const writtenQRef = useRef(urlQ)

  useEffect(() => {
    if (urlQ === writtenQRef.current) return
    writtenQRef.current = urlQ
    setQ(urlQ)
  }, [urlQ])

  const writeParams = useCallback((mutate: (params: URLSearchParams) => void) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      mutate(next)
      return next
    }, { replace: true })
  }, [setSearchParams])

  const trimmedQ = q.trim()
  useEffect(() => {
    if (trimmedQ === urlQ) return
    const timer = setTimeout(() => {
      writtenQRef.current = trimmedQ
      writeParams((params) => {
        if (trimmedQ) params.set('q', trimmedQ)
        else params.delete('q')
      })
    }, FILTER_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [trimmedQ, urlQ, writeParams])

  const setProvider = useCallback((value: string) => {
    writeParams((params) => {
      if (value) params.set('provider', value)
      else params.delete('provider')
    })
  }, [writeParams])

  return {
    q,
    setQ,
    provider,
    setProvider,
    filtersKey: modelsFiltersKey({ q, provider }),
  }
}
