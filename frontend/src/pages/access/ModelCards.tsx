import { Chip } from '@heroui/react'

import { ProviderMark } from '@/components/brand/ProviderMark'
import { StatusDot } from '@/components/ui/StatusDot'
import { useI18n } from '@/hooks/I18nContext'
import { modelCreditsText, modelIsFree } from '@/lib/format'
import { accountProviderLabel } from '@/lib/provider'

import { ModelActions } from './ModelActions'
import { ModelContextControls } from './ModelContextControls'
import { modelProvider, modelRegion, modelRowKey, modelSettingsKey, routedModelName, type ModelListProps } from './modelMeta'

/** 窄屏（<lg）模型目录卡片列表；桌面端由 ModelTable 承接。 */
export function ModelCards({ models, savingKey, onDetails, onToggleMaxMode, onReasoningChange }: ModelListProps) {
  const { t } = useI18n()
  return (
    <div className="divide-y divide-separator lg:hidden">
      {models.map((model) => {
        const provider = modelProvider(model)
        const region = modelRegion(model)
        const free = modelIsFree(model)
        const credits = modelCreditsText(model)
        const routed = routedModelName(model)
        return (
          <article key={modelRowKey(model)} className="space-y-3 px-4 py-4">
            <div className="flex items-start justify-between gap-3">
              <div className="flex min-w-0 items-start gap-3">
                <StatusDot state={model.stale ? 'warn' : 'ok'} className="mt-1.5" />
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <div className="truncate font-medium">{model.display_name || model.id}</div>
                    {free ? <Chip size="sm" variant="soft" color="success">{t('modelFree')}</Chip> : null}
                    {credits ? <span className="mono text-micro text-muted">{credits}</span> : null}
                  </div>
                  <div className="mono mt-0.5 truncate text-micro text-muted">{model.id}</div>
                  {routed ? <div className="mt-0.5 text-micro text-muted">{t('routedTo', { model: routed })}</div> : null}
                </div>
              </div>
              <Chip
                size="sm"
                variant="soft"
                color={model.context_custom ? 'warning' : model.stale ? 'warning' : 'success'}
              >
                {model.context_custom ? t('custom') : t('defaultValue')}
              </Chip>
            </div>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
              <span className="inline-flex items-center gap-1.5">
                <ProviderMark provider={provider} size={14} />
                <span className="font-medium">{accountProviderLabel(provider, region || undefined, t)}</span>
              </span>
              <span className="mono break-all text-muted">{model.mapped_key || model.native_model || model.id}</span>
            </div>
            <ModelContextControls
              model={model}
              saving={savingKey === modelSettingsKey(model)}
              onToggleMaxMode={onToggleMaxMode}
              onReasoningChange={onReasoningChange}
            />
            <ModelActions model={model} onDetails={onDetails} />
          </article>
        )
      })}
    </div>
  )
}
