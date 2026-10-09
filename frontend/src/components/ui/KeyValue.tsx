import type { ReactNode } from 'react'

export type KeyValueItem = {
  /** 字段名（dt）。 */
  label: ReactNode
  /** 字段值（dd）。 */
  value: ReactNode
}

type Props = {
  items: KeyValueItem[]
  className?: string
}

/**
 * 描述列表原语：详情区「字段名 → 字段值」的语义化呈现（dl/dt/dd）。
 *
 * 每个条目包在 div 中（HTML5 允许 dl > div > dt + dd），窄屏单列、
 * 640px 起两列；值允许换行，长 ID / URL 不会撑破容器。
 */
export function KeyValue({ items, className }: Props) {
  return (
    <dl className={['grid gap-x-6 gap-y-3 sm:grid-cols-2', className].filter(Boolean).join(' ')}>
      {items.map((item, index) => (
        <div key={typeof item.label === 'string' ? item.label : index} className="min-w-0">
          <dt className="text-xs text-muted">{item.label}</dt>
          <dd className="mt-0.5 text-sm break-words text-foreground">{item.value}</dd>
        </div>
      ))}
    </dl>
  )
}
