import type { ReactNode } from 'react'

/** 横幅语义（固定映射到状态色：info 中性、warning/success/danger 语义色）。 */
export type BannerStatus = 'info' | 'warning' | 'danger' | 'success'

type Props = {
  status: BannerStatus
  /** 主文案（一句话结论）。 */
  title: ReactNode
  /** 补充说明（可选）。 */
  description?: ReactNode
  /** 右侧操作插槽：跳转 / 处理按钮（可选）。 */
  actions?: ReactNode
  className?: string
}

const toneClasses: Record<BannerStatus, { box: string; title: string }> = {
  info: { box: 'border-border bg-surface-secondary', title: 'text-foreground' },
  warning: { box: 'border-warning/30 bg-warning/5', title: 'text-warning' },
  danger: { box: 'border-danger/30 bg-danger/5', title: 'text-danger' },
  success: { box: 'border-success/30 bg-success/5', title: 'text-success' },
}

/**
 * 页头告警横幅原语（配额告警复用）：状态色 + 标题 + 说明 + 右侧操作。
 *
 * 边界（方案 §6.5）：与 `PageAlert` 职责分离——Banner 是**页头横幅**
 * （区块级结论 + 操作入口），PageAlert 是**页内提示**（区块内错误态，
 * 基于 HeroUI Alert）。**不含 dismiss 能力**：需要消失的场景由调用方
 * 条件渲染（把「关闭」当状态管理会引入与 URL / 数据不同步的隐式状态）。
 */
export function Banner({ status, title, description, actions, className }: Props) {
  const tone = toneClasses[status]
  return (
    <section
      role="status"
      className={['rounded-2xl border px-4 py-3', tone.box, className].filter(Boolean).join(' ')}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className={['text-sm font-medium', tone.title].join(' ')}>{title}</div>
          {description ? <div className="mt-0.5 text-xs leading-5 text-muted">{description}</div> : null}
        </div>
        {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
    </section>
  )
}
