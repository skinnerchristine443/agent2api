import { Label, Switch } from '@heroui/react'

type Props = {
  isSelected: boolean
  isDisabled?: boolean
  ariaLabel: string
  label?: string
  size?: 'sm' | 'md' | 'lg'
  onChange: (selected: boolean) => void
}

export function CompactSwitch({
  isSelected,
  isDisabled,
  ariaLabel,
  label,
  size = 'sm',
  onChange,
}: Props) {
  // aria-label 必须落在 Switch 本体（RAC 的 input 上）；此前放在 sr-only span 里
  // 屏幕阅读器读不到（React Aria 只认自身 Label 组件），控制台会打 visible label 警告。
  return (
    <Switch size={size} isSelected={isSelected} isDisabled={isDisabled} aria-label={ariaLabel} onChange={onChange}>
      <Switch.Content>
        <Switch.Control>
          <Switch.Thumb />
        </Switch.Control>
        {label ? <Label>{label}</Label> : null}
      </Switch.Content>
    </Switch>
  )
}
