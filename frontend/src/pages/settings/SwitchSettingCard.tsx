import type { ReactNode } from 'react'

import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { SettingCard } from '@/components/ui/SettingCard'

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

/**
 * 开关型设置卡：`SettingCard`（形态 B）的开关特化——头部右侧为 `CompactSwitch`，
 * 底部为状态脚。保留为具名包装，避免各页重复拼装开关与状态脚。
 */
export function SwitchSettingCard({ icon, title, hint, isSelected, isDisabled, ariaLabel, onChange, statusLabel, statusValue }: Props) {
  return (
    <SettingCard
      icon={icon}
      title={title}
      hint={hint}
      right={<CompactSwitch isSelected={isSelected} isDisabled={isDisabled} ariaLabel={ariaLabel} onChange={onChange} />}
      statusLabel={statusLabel}
      statusValue={statusValue}
    />
  )
}
