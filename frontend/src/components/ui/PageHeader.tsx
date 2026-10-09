import type { ReactNode } from 'react'

type Props = {
  /** 页内说明文字（可选）：补充当前视图的口径 / 范围，不重复页面标题。 */
  description?: ReactNode
  /** 主操作插槽：页面级主按钮 / 刷新等（可选）。 */
  actions?: ReactNode
  className?: string
}

/**
 * 页内操作行原语：**描述 + 主操作插槽，不含页面级标题**。
 *
 * 分工（方案 §6.5）：页面标题由 AppHeader 统一渲染（取 `nav.ts` 的
 * 页面 key），本组件只承载页面内部的说明与操作按钮——两处都写标题会
 * 出现重复的两级标题（现存缺陷，逐页迁移时清除）。
 */
export function PageHeader({ description, actions, className }: Props) {
  return (
    <div className={['flex flex-wrap items-center justify-between gap-3', className].filter(Boolean).join(' ')}>
      <div className="min-w-0 text-sm text-muted">{description}</div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  )
}
