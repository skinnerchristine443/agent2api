import type { ReactNode } from 'react'

type Props = {
  /** 左侧筛选控件区：编排既有 `FilterSelect` / `FilterSearchSelect` / `FilterToggle`。 */
  children: ReactNode
  /** 右侧计数（如「显示 12 / 40 条」；建议由调用方传入已本地化的文本）。 */
  count?: ReactNode
  /** 右侧附加操作（如「清除筛选」按钮；可选）。 */
  actions?: ReactNode
  className?: string
}

/**
 * 筛选栏布局原语：左（筛选控件）+ 右（操作 + 计数）的响应式容器。
 *
 * 只做布局编排，**不重写**既有 `FilterSelect` / `FilterSearchSelect` /
 * `FilterToggle` 的交互逻辑（方案 §6.5：三者由本组件统一编排、不再散排）。
 * 控件在窄屏自动换行，计数与操作保持右对齐。
 */
export function FilterBar({ children, count, actions, className }: Props) {
  return (
    <div className={['flex flex-wrap items-center justify-between gap-3', className].filter(Boolean).join(' ')}>
      <div className="flex min-w-0 flex-wrap items-center gap-2">{children}</div>
      {actions || count ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {actions}
          {count ? <div className="mono text-xs text-muted">{count}</div> : null}
        </div>
      ) : null}
    </div>
  )
}
