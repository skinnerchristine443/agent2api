import type { ReactNode } from 'react'
import { Card } from '@heroui/react'

type Props = {
  /** 图标磁贴内容（15px 图标）。 */
  icon: ReactNode
  /** 卡片标题（渲染为 `<h3>`）。 */
  title: ReactNode
  /** 标题下方说明。 */
  hint?: ReactNode
  /** 头部右侧插槽（开关 / 徽标等；可选）。 */
  right?: ReactNode
  /** 底部状态脚：左标签 + 右值（可选）。 */
  statusLabel?: ReactNode
  statusValue?: ReactNode
  children?: ReactNode
  className?: string
}

/**
 * 设置磁贴卡原语（设计规范 v3「形态 B」）。
 *
 * 与 `SectionCard`（形态 A，区块卡）并列的**唯二卡片形态**之一——本形态用于
 * 「设置项 / 开关项」：信息密度低、需图标视觉锚点。头部固定为
 * **图标磁贴**（`size-8 rounded-lg bg-surface-secondary`）+ `<h3>` + 说明。
 *
 * ⚠️ 圆角：HeroUI `<Card>` 默认 24px，与规范「容器=16px」冲突 ⇒ **必须显式覆写
 * `rounded-2xl`**（v3 规范：取消 rounded-3xl）。
 */
export function SettingCard({
  icon,
  title,
  hint,
  right,
  statusLabel,
  statusValue,
  children,
  className,
}: Props) {
  return (
    <Card data-gsap-reveal className={['rounded-2xl', className].filter(Boolean).join(' ')}>
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-surface-secondary text-foreground">{icon}</div>
          <div className="min-w-0">
            <h3 className="font-semibold">{title}</h3>
            {hint ? <p className="mt-1 text-xs leading-5 text-muted">{hint}</p> : null}
          </div>
        </div>
        {right ? <div className="shrink-0">{right}</div> : null}
      </div>
      {children ? <div className="mt-4">{children}</div> : null}
      {statusLabel !== undefined && statusValue !== undefined ? (
        <div className="mt-4 flex items-center justify-between border-t border-separator pt-3 text-xs text-muted">
          <span>{statusLabel}</span>
          <span className="font-medium text-foreground">{statusValue}</span>
        </div>
      ) : null}
    </Card>
  )
}
