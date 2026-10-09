import { Tooltip } from '@heroui/react'

import type { ModelInfo } from '@/api/types'
import { formatTokens } from '@/components/models/ModelDetailsModal'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { FilterSelect } from '@/components/ui/FilterSelect'
import { useI18n } from '@/hooks/I18nContext'

import { modelProvider, reasoningLabel } from './modelMeta'

function HintLabel({ label, hint }: { label: string; hint: string }) {
  return (
    <Tooltip>
      <Tooltip.Trigger>
        <span className="cursor-help text-micro text-muted">{label}</span>
      </Tooltip.Trigger>
      <Tooltip.Content>
        <p className="max-w-xs text-xs leading-5">{hint}</p>
      </Tooltip.Content>
    </Tooltip>
  )
}

/**
 * 「上下文与推理」单元格（表格列与移动端卡片共用）：
 * 窗口展示 + Trae 更大上下文开关 + 推理档位选择（PATCH 就地生效）。
 */
export function ModelContextControls({
  model,
  saving,
  onToggleMaxMode,
  onReasoningChange,
}: {
  model: ModelInfo
  saving: boolean
  onToggleMaxMode: (model: ModelInfo, selected: boolean) => void
  onReasoningChange: (model: ModelInfo, next: string) => void
}) {
  const { t } = useI18n()
  const provider = modelProvider(model)
  const devWindow = model.catalog_context_length || model.context_length
  const maxWindow = model.catalog_context_length_max
  const hasMaxWindow = Boolean(maxWindow && maxWindow !== devWindow)
  const options = model.reasoning_options || []
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-3">
      <span className="mono text-xs text-muted">
        {formatTokens(devWindow)}{hasMaxWindow ? ` → ${formatTokens(maxWindow)}` : ''}
      </span>
      {provider === 'workbuddy' && hasMaxWindow ? (
        <HintLabel label={t('catalogWindow')} hint={t('workbuddyContextHint')} />
      ) : null}
      {provider === 'trae' && model.supports_max_mode ? (
        <div className="flex items-center gap-2">
          <CompactSwitch
            isSelected={Boolean(model.max_mode)}
            isDisabled={saving}
            ariaLabel={`${model.id} ${t('maxMode')}`}
            onChange={(selected) => onToggleMaxMode(model, selected)}
          />
          <HintLabel
            label={`${t('maxMode')}${maxWindow ? ` ${formatTokens(maxWindow)}` : ''}`}
            hint={t('maxModeHint')}
          />
        </div>
      ) : null}
      {options.length > 1 ? (
        <div className="flex min-w-0 items-center gap-2">
          <HintLabel label={t('reasoningLevels')} hint={t('reasoningHint')} />
          <FilterSelect
            className="min-w-28"
            ariaLabel={`${model.id} ${t('reasoningLevels')}`}
            value={model.reasoning_effort || model.reasoning_default || options[0] || ''}
            onChange={(next) => { if (next) onReasoningChange(model, next) }}
            options={options.map((level) => ({ id: level, label: reasoningLabel(t, level) }))}
          />
        </div>
      ) : options.length === 1 ? (
        <HintLabel label={`${t('reasoningLevels')} ${reasoningLabel(t, options[0])}`} hint={t('reasoningHint')} />
      ) : model.reasoning_type ? (
        <span className="text-micro text-muted">{t('reasoningFixed', { type: model.reasoning_type })}</span>
      ) : provider === 'trae' && !model.supports_max_mode ? (
        <HintLabel label={t('catalogWindow')} hint={t('catalogWindowHint')} />
      ) : null}
    </div>
  )
}
