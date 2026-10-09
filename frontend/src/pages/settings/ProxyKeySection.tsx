import { Button } from '@heroui/react'
import { Plugs } from '@phosphor-icons/react'
import { SectionCard } from '@/components/ui/SectionCard'
import { useI18n } from '@/hooks/I18nContext'

import { SecretValueRow } from './SecretValueRow'

type Props = {
  busy: boolean
  /** 已显式 reveal 的完整密钥（未 reveal 为 undefined）。 */
  revealed?: string
  onReveal: () => void
  onHide: () => void
  onRotate: () => void
}

/**
 * 调用密钥（数据面 `/v1/*`）：默认隐藏（占位点），可显式 reveal 查看/复制；
 * 拒绝修改——只能轮换出新值（旧钥立即失效）。
 */
export function ProxyKeySection({ busy, revealed, onReveal, onHide, onRotate }: Props) {
  const { t } = useI18n()
  return (
    <div data-gsap-reveal>
      <SectionCard
        title={t('keysProxyTitle')}
        hint={t('keysProxyHint')}
        right={<Plugs size={18} className="text-muted" />}
      >
        <SecretValueRow
          value={revealed || t('keysProxyHiddenPlaceholder')}
          revealed={Boolean(revealed)}
          onReveal={onReveal}
          onHide={onHide}
          t={t}
        />
        <p className="mt-2 rounded-lg border border-warning/25 bg-warning/5 px-3 py-2 text-xs leading-5 text-muted">{t('keysProxyReadHint')}</p>
        <p className="mt-3 text-xs leading-5 text-muted">{t('keysProxyRotateHint')}</p>
        <div className="mt-4 flex justify-end">
          <Button size="sm" variant="ghost" isPending={busy} onPress={onRotate}>{t('keysProxyRotate')}</Button>
        </div>
      </SectionCard>
    </div>
  )
}
