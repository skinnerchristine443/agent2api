import { Chip, Modal } from '@heroui/react'
import { X } from '@phosphor-icons/react'
import type { ModelInfo } from '@/api/types'
import { modelCreditsText, modelIsFree } from '@/lib/format'
import { accountProviderLabel, tieredTraeCaps } from '@/lib/provider'
import { reasoningLevelKey } from '@/i18n/messages'
import type { Translate } from '@/i18n/messages'

import { formatTokens } from './modelFormat'

type Props = {
  model: ModelInfo | null
  t: Translate
  onClose: () => void
}

export function ModelDetailsModal({ model, t, onClose }: Props) {
  if (!model) return null
  const options = model.reasoning_options || []
  const windowDev = model.catalog_context_length || model.default_context_length || model.context_length
  const windowMax = model.catalog_context_length_max
  const credits = modelCreditsText(model)
  const free = modelIsFree(model)
  const provider = String(model.provider || model.owned_by || '').trim().toLowerCase()
  const region = String(model.region || model.regions?.[0] || '').trim().toLowerCase()
  const providerLabel = accountProviderLabel(provider, region || undefined, t)
  // 解析当前 max-mode 开关所显示的档位，使上限与
  // 所选窗口一致，而不是在关闭开关后仍停留在 Max 档。
  const traeTier = provider === 'trae' ? tieredTraeCaps(model) : undefined
  return (
    <Modal.Root isOpen onOpenChange={(next: boolean) => { if (!next) onClose() }}>
      <Modal.Backdrop variant="blur">
        <Modal.Container size="md" scroll="inside">
          <Modal.Dialog>
            <Modal.Header className="items-start justify-between gap-4 px-5 pt-5">
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <div className="text-base font-semibold tracking-[-0.015em]">{model.display_name || model.id}</div>
                  {free ? <Chip size="sm" variant="soft" color="success">{t('modelFree')}</Chip> : null}
                </div>
                <div className="mono mt-1 text-micro text-muted">{model.id}</div>
                <div className="mt-1 text-micro text-muted">
                  {providerLabel}{credits ? ` · ${credits}` : ''}
                </div>
              </div>
              <Modal.CloseTrigger aria-label={t('close')} className="grid size-8 place-items-center rounded-lg text-muted hover:bg-surface-secondary">
                <X size={16} />
              </Modal.CloseTrigger>
            </Modal.Header>
            <Modal.Body className="space-y-4 px-5 pb-5">
              <section>
                <div className="text-xs font-medium text-muted">{t('contextWindowCol')}</div>
                <div className="mt-1 text-sm">{formatTokens(windowDev)}{windowMax && windowMax !== windowDev ? ` → ${formatTokens(windowMax)}` : ''}</div>
                {model.supports_max_mode ? <p className="mt-1 text-micro leading-5 text-muted">{t('maxModeHint')}</p> : null}
                {!model.supports_max_mode && windowMax && windowMax !== windowDev ? <p className="mt-1 text-micro leading-5 text-muted">{t('workbuddyContextHint')}</p> : null}
                {traeTier
                  ? (traeTier.prompt_max_tokens ? <div className="mt-1 text-micro text-muted">{t('promptMaxTokens')}: {formatTokens(traeTier.prompt_max_tokens)}</div> : null)
                  : (model.prompt_max_tokens ? <div className="mt-1 text-micro text-muted">{t('promptMaxTokens')}: {formatTokens(model.prompt_max_tokens)}</div> : null)}
                {traeTier
                  ? (traeTier.max_output_tokens ? <div className="text-micro text-muted">{t('maxOutputTokens')}: {formatTokens(traeTier.max_output_tokens)}</div> : null)
                  : (model.max_output_tokens ? <div className="text-micro text-muted">{t('maxOutputTokens')}: {formatTokens(model.max_output_tokens)}</div> : null)}
              </section>
              <section>
                <div className="text-xs font-medium text-muted">{t('reasoningLevels')}</div>
                {options.length ? (
                  <>
                    <p className="mt-1 text-micro leading-5 text-muted">{t('reasoningHint')}</p>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {options.map((level) => {
                        const key = reasoningLevelKey(level)
                        const label = key ? t(key) : level
                        return (
                          <Chip key={level} size="sm" variant="soft" color={level === (model.reasoning_effort || model.reasoning_default) ? 'success' : 'default'}>
                            {label}{level === model.reasoning_default ? ` · ${t('defaultValue')}` : ''}{level === model.reasoning_effort && level !== model.reasoning_default ? ` · ${t('custom')}` : ''}
                          </Chip>
                        )
                      })}
                    </div>
                  </>
                ) : (
                  <div className="mt-1 text-sm text-muted">
                    {model.reasoning_type ? t('reasoningFixed', { type: model.reasoning_type }) : t('noReasoningLevels')}
                  </div>
                )}
              </section>
            </Modal.Body>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal.Root>
  )
}
