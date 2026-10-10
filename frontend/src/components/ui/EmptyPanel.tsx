import type { ReactNode } from 'react'
import { EmptyState } from '@heroui/react'

type Props = {
  title: string
  hint?: string
  action?: ReactNode
  icon?: ReactNode
  /**
   * 尺寸档（规范 v3）：`default` = 页面级（`min-h-56`）；`sm` = 卡片内 / 模态内（`min-h-40`）。
   * 取代此前各页外部传 className 拼 `min-h-32/40/44/48/56/80` 的 5–6 种变体。
   */
  size?: 'default' | 'sm'
  /** 是否叠加虚线描边框（`rounded-2xl border border-dashed`）。 */
  bordered?: boolean
  className?: string
}

export function EmptyPanel({
  title,
  hint,
  action,
  icon,
  size = 'default',
  bordered = false,
  className,
}: Props) {
  const shape = bordered ? 'rounded-2xl border border-dashed border-border' : ''
  const minHeight = size === 'sm' ? 'min-h-40' : 'min-h-56'
  return (
    <div className={['grid place-items-center px-6 py-12 text-center', minHeight, shape, className].filter(Boolean).join(' ')}>
      <div className="max-w-sm">
        {icon ? <div className="mb-3 flex justify-center text-muted">{icon}</div> : null}
        <EmptyState className="p-0 text-sm font-medium text-foreground">{title}</EmptyState>
        {hint ? <p className="mt-1 text-xs leading-5 text-muted">{hint}</p> : null}
        {action ? <div className="mt-5 flex justify-center">{action}</div> : null}
      </div>
    </div>
  )
}
