import { useEffect, useState } from 'react'
import { Button, Chip, Description, Input } from '@heroui/react'

import type { SystemSettings } from '@/api/system'
import { FormRow } from '@/components/ui/FormRow'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import type { Translate } from '@/i18n/messages'

import {
  daysToSeconds,
  normalizeWindowSeconds,
  parseWindowDays,
  secondsToDaysInput,
} from './expiryModel'

type Props = {
  settings: SystemSettings | null
  saving: boolean
  error: string | null
  onSave: (primarySeconds: number, secondarySeconds: number) => void
  t: Translate
}

/**
 * 到期窗口设置卡（自系统页迁出，新增「主=0 同存为 0」的显式语义）：
 * 主窗口（天，默认 3）/ 次窗口（天，默认 7）+ 保存。主窗口填 0 即关闭整套
 * 到期排序——次窗口置灰并同存为 0（与后端归一化一致）。
 */
export function ExpiryWindowCard({ settings, saving, error, onSave, t }: Props) {
  const [primaryDraft, setPrimaryDraft] = useState('3')
  const [secondaryDraft, setSecondaryDraft] = useState('7')

  const storedPrimary = settings?.expiry_window_seconds
  const storedSecondary = settings?.secondary_expiry_window_seconds

  // 仅在服务端值变化时用其重置草稿（加载 / 成功保存），使进行中的编辑
  // 不会被无关的 settings 更新覆盖。
  useEffect(() => {
    if (settings) setPrimaryDraft(secondsToDaysInput(settings.expiry_window_seconds))
  }, [storedPrimary]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (settings) setSecondaryDraft(secondsToDaysInput(settings.secondary_expiry_window_seconds))
  }, [storedSecondary]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!settings) {
    return (
      <SectionCard title={t('expiryWindowTitle')} hint={t('expiryWindowHint')}>
        <SkeletonBlock className="h-24 w-full" />
      </SectionCard>
    )
  }

  const primaryDays = parseWindowDays(primaryDraft)
  const secondaryDays = parseWindowDays(secondaryDraft)
  const valid = primaryDays != null && secondaryDays != null
  const sortDisabled = valid && primaryDays === 0
  const draft = valid
    ? normalizeWindowSeconds(daysToSeconds(primaryDays), daysToSeconds(secondaryDays))
    : null
  const dirty = draft != null && (
    draft.primarySeconds !== settings.expiry_window_seconds ||
    draft.secondarySeconds !== settings.secondary_expiry_window_seconds
  )

  return (
    <SectionCard
      title={t('expiryWindowTitle')}
      hint={t('expiryWindowHint')}
      right={sortDisabled ? <Chip size="sm" variant="soft" color="warning">{t('expirySortDisabled')}</Chip> : null}
    >
      <div className="space-y-4">
        {error ? <PageAlert title={error} /> : null}
        <FormRow label={t('expiryWindowPrimaryLabel')} hint={t('expiryWindowPrimaryHint')}>
          <Input
            type="number"
            min={0}
            step={1}
            inputMode="numeric"
            value={primaryDraft}
            disabled={saving}
            aria-label={t('expiryWindowPrimaryLabel')}
            onChange={(event) => setPrimaryDraft(event.target.value)}
          />
        </FormRow>
        <FormRow label={t('expiryWindowSecondaryLabel')} hint={t('expiryWindowSecondaryHint')}>
          <Input
            type="number"
            min={0}
            step={1}
            inputMode="numeric"
            value={sortDisabled ? '0' : secondaryDraft}
            disabled={saving || sortDisabled}
            aria-label={t('expiryWindowSecondaryLabel')}
            onChange={(event) => setSecondaryDraft(event.target.value)}
          />
        </FormRow>
        <Description className="text-xs leading-5 text-warning">{t('expiryWindowDisabledHint')}</Description>
        <Description className="text-xs leading-5 text-muted">{t('expiryWindowLayerHint')}</Description>
        {!valid ? <Description className="text-xs leading-5 text-danger">{t('expiryWindowInvalidHint')}</Description> : null}
        <div className="flex justify-end">
          <Button
            variant="secondary"
            isDisabled={!valid || !dirty || saving}
            isPending={saving}
            onPress={() => {
              if (!draft) return
              onSave(draft.primarySeconds, draft.secondarySeconds)
            }}
          >
            {t('expiryWindowSave')}
          </Button>
        </div>
      </div>
    </SectionCard>
  )
}
