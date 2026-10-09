import { useState } from 'react'
import { Button, Modal } from '@heroui/react'
import { Copy } from '@phosphor-icons/react'
import { useI18n } from '@/hooks/I18nContext'
import { copyText } from '@/lib/clipboard'
import type { RotatedKey } from '@/hooks/useApiKeys'

type Props = {
  /** 刚轮换出的新钥（null = 关闭）。 */
  rotated: RotatedKey | null
  onClose: () => void
}

/**
 * 新钥一次性展示（四件套 ③「显式保存确认」）：明文只在此处出现一次，且
 * **未点「我已保存新钥」不得关闭**——遮罩点击与 Esc 都被忽略，避免误关后
 * 再也取不回（GET 只回指纹）。关闭动作同时由调用方清空明文状态。
 */
export function SecretRevealModal({ rotated, onClose }: Props) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const secret = rotated?.secret || ''

  return (
    <Modal.Root isOpen={Boolean(secret)} onOpenChange={() => { /* 未确认保存前不响应任何关闭请求 */ }}>
      <Modal.Backdrop variant="blur" isDismissable={false}>
        <Modal.Container placement="center" size="lg">
          <Modal.Dialog>
            <Modal.Header className="items-start justify-between gap-4 px-6 pt-6">
              <div>
                <Modal.Heading className="text-xl font-semibold">
                  {t(rotated?.target === 'proxy' ? 'keysSecretTargetProxy' : 'consoleKeySecretTitle')}
                </Modal.Heading>
                <p className="mt-1.5 text-sm leading-6 text-muted">{t('keysSecretOnceHint')}</p>
              </div>
            </Modal.Header>
            <Modal.Body className="px-6 pb-2">
              <code className="mono block break-all rounded-lg border border-separator bg-surface-secondary px-3 py-3 text-sm">{secret}</code>
              {rotated?.prefix ? (
                <p className="mt-2 text-xs text-muted">
                  {t('keysRotatedPrefix')}: <span className="mono">{rotated.prefix}</span>
                </p>
              ) : null}
            </Modal.Body>
            <Modal.Footer className="justify-end">
              <Button
                variant="ghost"
                onPress={() => {
                  void copyText(secret)
                  setCopied(true)
                  window.setTimeout(() => setCopied(false), 1200)
                }}
              >
                <Copy size={14} />{copied ? t('copied') : t('copy')}
              </Button>
              <Button onPress={onClose}>{t('keysSecretSaved')}</Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal.Root>
  )
}
