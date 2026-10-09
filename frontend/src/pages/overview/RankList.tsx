import { useLayoutEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import gsap from 'gsap'

import { ProviderMark } from '@/components/brand/ProviderMark'

export type RankItem = {
  key: string
  label: string
  count: number
  meta?: string
  mark?: string
  /** 可选跳转：给定后整行变为链接（目标页带预筛选参数，如 `/logs/requests?status=error`）。 */
  to?: string
}

/** 排行榜列表（条形按 peak 归一；GSAP 入场动画保序，reduced-motion 下关闭；`to` 使整行可点）。 */
export function RankList({ items, empty, danger }: { items: RankItem[]; empty: string; danger?: boolean }) {
  const rootRef = useRef<HTMLDivElement>(null)
  const peak = Math.max(1, ...items.map((item) => item.count))
  const signature = items.map((item) => `${item.key}:${item.count}`).join('|')

  useLayoutEffect(() => {
    const root = rootRef.current
    if (!root) return
    const context = gsap.context(() => {
      const media = gsap.matchMedia()
      media.add('(prefers-reduced-motion: reduce)', () => {
        gsap.set('[data-rank-fill]', { clearProps: 'transform' })
      })
      media.add('(prefers-reduced-motion: no-preference)', () => {
        const fills = gsap.utils.toArray<HTMLElement>('[data-rank-fill]')
        if (!fills.length) return
        gsap.fromTo(fills, { scaleX: 0 }, {
          scaleX: 1,
          duration: 0.38,
          ease: 'power2.out',
          stagger: 0.05,
          transformOrigin: '0% 50%',
        })
      })
    }, root)
    return () => context.revert()
  }, [signature])

  if (!items.length) {
    return <div className="px-5 py-8 text-sm text-muted">{empty}</div>
  }
  return (
    <div ref={rootRef} className="divide-y divide-separator">
      {items.map((item) => {
        const body = (
          <>
            <div className="flex items-baseline justify-between gap-3">
              <div className="flex min-w-0 items-center gap-2">
                {item.mark ? <ProviderMark provider={item.mark} size={14} /> : null}
                <div className="truncate text-sm font-medium">{item.label}</div>
              </div>
              <div className="mono shrink-0 text-micro text-muted">
                {item.meta ? `${item.count} · ${item.meta}` : item.count}
              </div>
            </div>
            <div className="mt-2 h-1 overflow-hidden rounded-[2px] bg-separator">
              <div
                data-rank-fill
                className={`h-full origin-left rounded-[2px] ${danger ? 'bg-danger' : 'bg-success'}`}
                style={{ width: `${Math.max(6, (item.count / peak) * 100)}%` }}
              />
            </div>
          </>
        )
        return item.to ? (
          <Link
            key={item.key}
            to={item.to}
            className="block px-5 py-3 transition-colors hover:bg-surface-secondary"
          >
            {body}
          </Link>
        ) : (
          <div key={item.key} className="px-5 py-3">
            {body}
          </div>
        )
      })}
    </div>
  )
}
