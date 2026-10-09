import type { ReactNode } from 'react'

type Props = {
  /** 卡片标题（文本或节点）。 */
  title: ReactNode
  /** 标题下方的说明文字（可选）。 */
  hint?: ReactNode
  /** 卡片头右侧插槽：操作按钮 / 链接 / 计数（可选）。 */
  right?: ReactNode
  children: ReactNode
  /** 内容区是否带内边距。表格类区块传 `false`，由表格自行留白。默认 `true`。 */
  padded?: boolean
  className?: string
}

/**
 * 区块卡片原语：统一「卡片头（标题 + 说明 + 右侧插槽）+ 内容区」。
 *
 * 视觉对齐现有卡片：`rounded-2xl border border-border bg-surface`，
 * 内外分隔线用 `border-separator`。替代各页「手写 section + 头部 div」
 * 的重复模式（方案 §6.5 新增原语矩阵）。
 */
export function SectionCard({ title, hint, right, children, padded = true, className }: Props) {
  return (
    <section className={['overflow-hidden rounded-2xl border border-border bg-surface', className].filter(Boolean).join(' ')}>
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-separator px-4 py-3">
        <div className="min-w-0">
          <div className="text-sm font-semibold text-foreground">{title}</div>
          {hint ? <div className="mt-0.5 text-xs text-muted">{hint}</div> : null}
        </div>
        {right ? <div className="flex shrink-0 flex-wrap items-center gap-2">{right}</div> : null}
      </div>
      <div className={padded ? 'p-4' : undefined}>{children}</div>
    </section>
  )
}
