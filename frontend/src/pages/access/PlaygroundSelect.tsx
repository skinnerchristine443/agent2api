import type { ReactNode } from 'react'
import { Description, Label, ListBox, Select } from '@heroui/react'

export type PlaygroundOption = {
  id: string
  textValue: string
  label: string
  hint?: string
  icon?: ReactNode
}

/** 调试台的富选项下拉（账号 / 模型）：图标 + 标题 + 副标题。 */
export function PlaygroundSelect({
  label,
  value,
  onChange,
  placeholder,
  isDisabled,
  options,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  isDisabled?: boolean
  options: PlaygroundOption[]
}) {
  const selected = options.find((option) => option.id === value)
  return (
    <Select
      fullWidth
      value={value || null}
      placeholder={placeholder}
      isDisabled={isDisabled}
      onChange={(next) => {
        if (typeof next === 'string' && next) onChange(next)
      }}
    >
      <Label className="text-sm font-medium text-muted">{label}</Label>
      <Select.Trigger className="items-center">
        <Select.Value className="min-w-0 truncate">
          {({ defaultChildren, isPlaceholder }) => {
            if (isPlaceholder || !selected) return defaultChildren
            return (
              <span className="flex min-w-0 items-center gap-2">
                {selected.icon ? <span className="grid size-5 shrink-0 place-items-center">{selected.icon}</span> : null}
                <span className="truncate">{selected.label}</span>
              </span>
            )
          }}
        </Select.Value>
        <Select.Indicator />
      </Select.Trigger>
      <Select.Popover className="max-h-72">
        <ListBox>
          {options.map((option) => (
            <ListBox.Item key={option.id} id={option.id} textValue={option.textValue}>
              {option.icon ? <span className="grid size-5 shrink-0 place-items-center">{option.icon}</span> : null}
              <div className="min-w-0 flex-1">
                <Label className="block truncate">{option.label}</Label>
                {option.hint ? <Description className="truncate">{option.hint}</Description> : null}
              </div>
              <ListBox.ItemIndicator />
            </ListBox.Item>
          ))}
        </ListBox>
      </Select.Popover>
    </Select>
  )
}
