import type { HTMLAttributes, ReactNode } from 'react'

type Props = {
  /** 卡片标题（文本或节点）。**省略则不渲染头部栏**（无标题容器，如日志表）。 */
  title?: ReactNode
  /** 标题下方的说明文字（可选）。 */
  hint?: ReactNode
  /** 卡片头右侧插槽：操作按钮 / 链接 / 计数（可选）。 */
  right?: ReactNode
  children: ReactNode
  /** 内容区是否带内边距。表格类区块传 `false`，由表格自行留白。默认 `true`。 */
  padded?: boolean
} & Omit<HTMLAttributes<HTMLElement>, 'children' | 'title'>

/**
 * 区块卡片原语（设计规范 v3「形态 A」）：统一「卡片头（标题 + 说明 + 右侧插槽）+
 * 内容区」，`rounded-2xl border border-border bg-surface`，内外分隔线用 `border-separator`。
 *
 * 与 `SettingCard`（形态 B，设置磁贴卡）并列的唯二卡片形态。**无 `title` 时不渲染头部栏**
 * ——供「纯容器」场景（如日志表格卡，上下文已由页头/页签给出）。
 * 其余原生属性（`className` / `aria-*` / `data-*` 等）透传到根 `<section>`。
 */
export function SectionCard({ title, hint, right, children, padded = true, className, ...rest }: Props) {
  const hasHeader = title !== undefined || hint !== undefined || right !== undefined
  return (
    <section className={['overflow-hidden rounded-2xl border border-border bg-surface', className].filter(Boolean).join(' ')} {...rest}>
      {hasHeader ? (
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-separator px-4 py-3">
          <div className="min-w-0">
            {title !== undefined ? <div className="text-sm font-semibold text-foreground">{title}</div> : null}
            {hint ? <div className="mt-0.5 text-xs text-muted">{hint}</div> : null}
          </div>
          {right ? <div className="flex shrink-0 flex-wrap items-center gap-2">{right}</div> : null}
        </div>
      ) : null}
      <div className={padded ? 'p-4' : undefined}>{children}</div>
    </section>
  )
}
