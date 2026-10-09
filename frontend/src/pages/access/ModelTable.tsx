import { Chip, Table } from '@heroui/react'

import { ProviderMark } from '@/components/brand/ProviderMark'
import { StatusDot } from '@/components/ui/StatusDot'
import { useI18n } from '@/hooks/I18nContext'
import { modelCreditsText, modelIsFree } from '@/lib/format'
import { accountProviderLabel } from '@/lib/provider'

import { ModelActions } from './ModelActions'
import { ModelContextControls } from './ModelContextControls'
import { modelProvider, modelRegion, modelRowKey, modelSettingsKey, routedModelName, type ModelListProps } from './modelMeta'

/** 桌面端（≥lg）模型目录表格；窄屏由 ModelCards 承接。 */
export function ModelTable({ models, savingKey, onDetails, onToggleMaxMode, onReasoningChange }: ModelListProps) {
  const { t } = useI18n()
  return (
    <Table className="hidden min-w-0 lg:block">
      <Table.ScrollContainer className="min-w-0 overflow-x-auto">
        <Table.Content aria-label={t('availableModels')} className="min-w-[68rem]">
          <Table.Header>
            <Table.Column isRowHeader>{t('modelCol')}</Table.Column>
            <Table.Column>{t('modelIdCol')}</Table.Column>
            <Table.Column>{t('providerCol')}</Table.Column>
            <Table.Column>{t('upstreamKeyCol')}</Table.Column>
            <Table.Column>{t('modelDefaultsCol')}</Table.Column>
            <Table.Column width={96}>{t('stateCol')}</Table.Column>
            <Table.Column>{t('actions')}</Table.Column>
          </Table.Header>
          <Table.Body>
            {models.map((model) => {
              const provider = modelProvider(model)
              const region = modelRegion(model)
              const free = modelIsFree(model)
              const credits = modelCreditsText(model)
              const routed = routedModelName(model)
              return (
                <Table.Row key={modelRowKey(model)}>
                  <Table.Cell>
                    <div className="flex items-center gap-3 py-1">
                      <StatusDot state={model.stale ? 'warn' : 'ok'} />
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <div className="font-medium">{model.display_name || model.id}</div>
                          {free ? <Chip size="sm" variant="soft" color="success">{t('modelFree')}</Chip> : null}
                          {credits ? <span className="mono text-micro text-muted">{credits}</span> : null}
                        </div>
                        {routed ? <div className="mt-0.5 text-micro text-muted">{t('routedTo', { model: routed })}</div> : null}
                      </div>
                    </div>
                  </Table.Cell>
                  <Table.Cell><span className="mono text-xs font-medium">{model.id}</span></Table.Cell>
                  <Table.Cell>
                    <div className="flex items-center gap-2">
                      <ProviderMark provider={provider} size={14} />
                      <span className="text-xs font-medium">{accountProviderLabel(provider, region || undefined, t)}</span>
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    <span className="mono text-xs text-muted">{model.mapped_key || model.native_model || model.id}</span>
                  </Table.Cell>
                  <Table.Cell>
                    <ModelContextControls
                      model={model}
                      saving={savingKey === modelSettingsKey(model)}
                      onToggleMaxMode={onToggleMaxMode}
                      onReasoningChange={onReasoningChange}
                    />
                  </Table.Cell>
                  <Table.Cell>
                    <Chip
                      size="sm"
                      variant="soft"
                      className="whitespace-nowrap"
                      color={model.context_custom ? 'warning' : model.stale ? 'warning' : 'success'}
                    >
                      {model.context_custom ? t('custom') : t('defaultValue')}
                    </Chip>
                  </Table.Cell>
                  <Table.Cell><ModelActions model={model} onDetails={onDetails} /></Table.Cell>
                </Table.Row>
              )
            })}
          </Table.Body>
        </Table.Content>
      </Table.ScrollContainer>
    </Table>
  )
}
