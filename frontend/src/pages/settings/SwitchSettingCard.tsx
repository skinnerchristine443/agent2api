import { Card } from '@heroui/react'
import type { ReactNode } from 'react'

import { CompactSwitch } from '@/components/ui/CompactSwitch'

type Props = {
  icon: ReactNode
  title: ReactNode
  hint: ReactNode
  isSelected: boolean
  isDisabled: boolean
  ariaLabel: string
  onChange: (selected: boolean) => void
  statusLabel: ReactNode
  statusValue: ReactNode
}

/** 开关型设置卡（模型池 / 停用账号签到）：结构一致，仅文案与开关绑定不同。 */
export function SwitchSettingCard({ icon, title, hint, isSelected, isDisabled, ariaLabel, onChange, statusLabel, statusValue }: Props) {
  return (
    <Card data-gsap-reveal>
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-surface-secondary text-foreground">{icon}</div>
          <div>
            <h3 className="font-semibold">{title}</h3>
            <p className="mt-1 text-xs leading-5 text-muted">{hint}</p>
          </div>
        </div>
        <CompactSwitch
          isSelected={isSelected}
          isDisabled={isDisabled}
          ariaLabel={ariaLabel}
          onChange={onChange}
        />
      </div>
      <div className="mt-4 flex items-center justify-between border-t border-separator pt-3 text-xs text-muted">
        <span>{statusLabel}</span>
        <span className="font-medium text-foreground">{statusValue}</span>
      </div>
    </Card>
  )
}
