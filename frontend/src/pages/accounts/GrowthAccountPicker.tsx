import { Description, Label, ListBox, Select } from '@heroui/react'

import { ProviderMark } from '@/components/brand/ProviderMark'
import type { Translate } from '@/i18n/messages'
import type { AccountRow } from '@/lib/account'
import { accountProviderLabel } from '@/lib/provider'

type Props = {
  accounts: AccountRow[]
  value: string
  /** 本会话内已探明「不支持成长中心」的渠道（选择器置灰并注明原因）。 */
  unsupportedProviders: Record<string, boolean>
  onChange: (id: string) => void
  t: Translate
}

/**
 * 成长中心账号选择器。
 *
 * 能力门控：**静态过滤在页面层**——调用方（GrowthPage）已按
 * `/api/providers` 的 `capabilities.growth` 只传入支持成长中心的渠道账号；
 * 本组件内的 `unsupportedProviders` 置灰是动态兜底（请求仍返回
 * provider_unsupported 时的本会话缓存）。
 */
export function GrowthAccountPicker({ accounts, value, unsupportedProviders, onChange, t }: Props) {
  const options = accounts.map((account) => {
    const provider = String(account.provider || '').toLowerCase()
    const unsupported = Boolean(unsupportedProviders[provider])
    return {
      id: account.id,
      provider,
      textValue: `${account.name || account.id} ${provider}`,
      label: account.name || account.id,
      hint: unsupported ? t('growthUnsupported') : accountProviderLabel(account.provider, account.region, t),
      disabled: unsupported,
    }
  })
  const selected = options.find((option) => option.id === value)

  return (
    <Select
      className="min-w-56"
      value={value || null}
      placeholder={t('growthAccountPlaceholder')}
      isDisabled={options.length === 0}
      onChange={(next) => {
        if (typeof next === 'string' && next) onChange(next)
      }}
    >
      <Label className="sr-only">{t('growthAccountLabel')}</Label>
      <Select.Trigger className="items-center">
        <Select.Value className="min-w-0 truncate">
          {({ defaultChildren, isPlaceholder }) => {
            if (isPlaceholder || !selected) return defaultChildren
            return (
              <span className="flex min-w-0 items-center gap-2">
                <span className="grid size-5 shrink-0 place-items-center"><ProviderMark provider={selected.provider} size={16} /></span>
                <span className="truncate">{selected.label}</span>
              </span>
            )
          }}
        </Select.Value>
        <Select.Indicator />
      </Select.Trigger>
      <Select.Popover className="max-h-72 min-w-56">
        <ListBox>
          {options.map((option) => (
            <ListBox.Item key={option.id} id={option.id} textValue={option.textValue} isDisabled={option.disabled}>
              <span className="grid size-5 shrink-0 place-items-center"><ProviderMark provider={option.provider} size={16} /></span>
              <div className="min-w-0 flex-1">
                <Label className="block truncate">{option.label}</Label>
                <Description className="truncate">{option.hint}</Description>
              </div>
              <ListBox.ItemIndicator />
            </ListBox.Item>
          ))}
        </ListBox>
      </Select.Popover>
    </Select>
  )
}
