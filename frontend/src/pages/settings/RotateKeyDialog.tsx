import type { KeyTarget } from '@/api/keys'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { useI18n } from '@/hooks/I18nContext'

type Props = {
  /** 待确认的轮换目标（null = 关闭）。 */
  target: KeyTarget | null
  busy: boolean
  onClose: () => void
  onConfirm: () => void
}

/** 轮换二次确认（四件套 ④）：按目标给出不同的失效范围说明。 */
export function RotateKeyDialog({ target, busy, onClose, onConfirm }: Props) {
  const { t } = useI18n()
  const isProxy = target === 'proxy'
  return (
    <ConfirmDialog
      isOpen={target != null}
      title={t(isProxy ? 'keysProxyRotate' : 'consoleKeyRotate')}
      description={t(isProxy ? 'keysProxyRotateConfirm' : 'keysConsoleRotateConfirm')}
      confirmLabel={t('consoleKeyRotateNow')}
      cancelLabel={t('cancel')}
      closeLabel={t('close')}
      isPending={busy}
      status="warning"
      confirmVariant={isProxy ? 'danger' : 'primary'}
      onClose={onClose}
      onConfirm={onConfirm}
    />
  )
}
