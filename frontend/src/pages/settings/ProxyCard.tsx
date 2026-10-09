import { Card, Description, Input, Label } from '@heroui/react'
import { SlidersHorizontal } from '@phosphor-icons/react'

import { useI18n } from '@/hooks/I18nContext'

type Props = {
  draft: string
  disabled: boolean
  onDraftChange: (value: string) => void
  onCommit: (value: string) => void
}

/** 全局代理出口卡（失焦提交；字段未变化时上游跳过 PATCH）。 */
export function ProxyCard({ draft, disabled, onDraftChange, onCommit }: Props) {
  const { t } = useI18n()
  return (
    <Card data-gsap-reveal>
      <div className="flex items-start gap-3">
        <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-surface-secondary text-foreground"><SlidersHorizontal size={15} /></div>
        <div>
          <h3 className="font-semibold">{t('proxySettingsTitle')}</h3>
          <p className="mt-1 text-xs leading-5 text-muted">{t('proxySettingsHint')}</p>
        </div>
      </div>
      <div className="mt-4 space-y-1.5">
        <Label className="text-sm font-medium text-muted">{t('proxyUrl')}</Label>
        <Input
          value={draft}
          onChange={(event) => onDraftChange(event.target.value)}
          onBlur={(event) => onCommit(event.target.value.trim())}
          placeholder={t('proxyUrlPlaceholder')}
          disabled={disabled}
        />
        <Description className="text-xs leading-5 text-muted">{t('proxyUrlHint')}</Description>
      </div>
    </Card>
  )
}
