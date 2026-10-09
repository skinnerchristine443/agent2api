import { Button } from '@heroui/react'
import { Key } from '@phosphor-icons/react'
import type { ConsoleKeyView } from '@/api/keys'
import { SectionCard } from '@/components/ui/SectionCard'
import { useI18n } from '@/hooks/I18nContext'

import { SecretValueRow } from './SecretValueRow'

type Props = {
  consoleKey: ConsoleKeyView | null
  busy: boolean
  /** 已显式 reveal 的完整密钥（未 reveal 为 undefined）。 */
  revealed?: string
  onReveal: () => void
  onHide: () => void
  onRotate: () => void
}

/**
 * 控制台密钥（管理面 `/api/*`）：指纹/完整值 + 复制 + 轮换（未 reveal 时只显示
 * 指纹；reveal 为显式只读动作，见 useApiKeys）。
 */
export function ConsoleKeySection({ consoleKey, busy, revealed, onReveal, onHide, onRotate }: Props) {
  const { t } = useI18n()
  return (
    <div data-gsap-reveal>
      <SectionCard
        title={t('consoleKeyTitle')}
        hint={t('consoleKeyHint')}
        right={<Key size={18} className="text-muted" />}
      >
        <SecretValueRow
          value={revealed || consoleKey?.prefix || '—'}
          revealed={Boolean(revealed)}
          onReveal={onReveal}
          onHide={onHide}
          t={t}
        />
        <p className="mt-2 text-xs leading-5 text-muted">{t('keysReadOnlyHint')}</p>
        <p className="mt-2 text-xs leading-5 text-muted">{t('consoleKeyLead')}</p>
        <div className="mt-4 flex justify-end">
          <Button size="sm" variant="ghost" isPending={busy} onPress={onRotate}>{t('consoleKeyRotate')}</Button>
        </div>
      </SectionCard>
    </div>
  )
}
