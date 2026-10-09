import { CaretRight } from '@phosphor-icons/react'
import { Input, NumberField } from '@heroui/react'

import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { FormRow } from '@/components/ui/FormRow'
import type { Translate } from '@/i18n/messages'

type Props = {
  name: string
  onNameChange: (value: string) => void
  maxInFlight: number
  onMaxInFlightChange: (value: number) => void
  priority: number
  onPriorityChange: (value: number) => void
  proxyUrl: string
  onProxyUrlChange: (value: string) => void
  dropSystemPrompt: boolean
  onDropSystemPromptChange: (value: boolean) => void
  showDropSystem: boolean
  locked: boolean
  advancedOpen: boolean
  onToggleAdvanced: () => void
  t: Translate
}

/** 添加向导的账号设置（名称 + 高级选项；登录前锁定，创建后不可改）。 */
export function AddAccountSettings({
  name,
  onNameChange,
  maxInFlight,
  onMaxInFlightChange,
  priority,
  onPriorityChange,
  proxyUrl,
  onProxyUrlChange,
  dropSystemPrompt,
  onDropSystemPromptChange,
  showDropSystem,
  locked,
  advancedOpen,
  onToggleAdvanced,
  t,
}: Props) {
  return (
    <div className="mt-4 space-y-3">
      <FormRow label={t('accountName')}>
        <Input
          value={name}
          onChange={(event) => onNameChange(event.target.value)}
          placeholder={t('wizardNamePh')}
          aria-label={t('accountName')}
          disabled={locked}
          autoFocus
        />
      </FormRow>
      <button
        type="button"
        onClick={onToggleAdvanced}
        aria-expanded={advancedOpen}
        className="inline-flex items-center gap-1 text-xs font-medium text-muted transition-colors hover:text-foreground"
      >
        <CaretRight size={12} className={`transition-transform duration-200 ${advancedOpen ? 'rotate-90' : ''}`} />
        {t('wizardAdvanced')}
      </button>
      {advancedOpen ? (
        <div className="space-y-3 rounded-lg border border-separator px-3.5 py-3.5">
          <FormRow label={t('maxInflight')} hint={t('maxInflightHint')}>
            <NumberField
              value={maxInFlight}
              onChange={(value) => onMaxInFlightChange(value ?? 4)}
              minValue={1}
              maxValue={32}
              isDisabled={locked}
              isRequired
              aria-label={t('maxInflight')}
            >
              <NumberField.Group>
                <NumberField.DecrementButton />
                <NumberField.Input />
                <NumberField.IncrementButton />
              </NumberField.Group>
            </NumberField>
          </FormRow>
          <FormRow label={t('priority')} hint={t('priorityHint')}>
            <NumberField
              value={priority}
              onChange={(value) => onPriorityChange(value ?? 50)}
              minValue={1}
              maxValue={100}
              isDisabled={locked}
              isRequired
              aria-label={t('priority')}
            >
              <NumberField.Group>
                <NumberField.DecrementButton />
                <NumberField.Input />
                <NumberField.IncrementButton />
              </NumberField.Group>
            </NumberField>
          </FormRow>
          <FormRow label={t('proxyUrl')} hint={t('proxyUrlHint')}>
            <Input
              value={proxyUrl}
              onChange={(event) => onProxyUrlChange(event.target.value)}
              placeholder={t('proxyUrlPlaceholder')}
              aria-label={t('proxyUrl')}
              disabled={locked}
            />
          </FormRow>
          {showDropSystem ? (
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <div className="text-sm font-medium text-muted">{t('dropSystemPrompt')}</div>
                <p className="mt-0.5 text-xs leading-5 text-muted">{t('dropSystemPromptCreateHint')}</p>
              </div>
              <CompactSwitch
                isSelected={dropSystemPrompt}
                isDisabled={locked}
                ariaLabel={t('dropSystemPrompt')}
                onChange={onDropSystemPromptChange}
              />
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
