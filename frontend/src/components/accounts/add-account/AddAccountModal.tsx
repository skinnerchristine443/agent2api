import { useLayoutEffect, useRef } from 'react'
import gsap from 'gsap'
import { Button, Modal } from '@heroui/react'
import { X } from '@phosphor-icons/react'

import { useI18n } from '@/hooks/I18nContext'

import { AddAccountLoginStep } from './AddAccountLoginStep'
import { AddAccountMethodStep } from './AddAccountMethodStep'
import { useAddAccountWizard } from './useAddAccountWizard'

type Props = {
  isOpen: boolean
  onClose: () => void
  onAdded: () => void
  /** 预选渠道（批次 9：账号池页签联动；无效值回落首项）。 */
  presetProvider?: string
}

/**
 * 添加账号向导模态（壳）：
 * 步骤内容在 AddAccountMethodStep / AddAccountLoginStep，状态机在 useAddAccountWizard，
 * 设备登录等待在 useDeviceLoginPoll。
 */
export function AddAccountModal({ isOpen, onClose, onAdded, presetProvider }: Props) {
  const { t } = useI18n()
  const wizard = useAddAccountWizard({ isOpen, onClose, onAdded, presetProvider })
  const stepBody = useRef<HTMLDivElement>(null)

  useLayoutEffect(() => {
    const body = stepBody.current
    if (!body || !isOpen) return
    const context = gsap.context(() => {
      const media = gsap.matchMedia()
      media.add('(prefers-reduced-motion: reduce)', () => {
        gsap.set(body, { autoAlpha: 1, y: 0 })
      })
      media.add('(prefers-reduced-motion: no-preference)', () => {
        gsap.fromTo(
          body,
          { autoAlpha: 0, y: 12 },
          { autoAlpha: 1, y: 0, duration: 0.34, ease: 'power3.out', overwrite: true },
        )
      })
    }, body)
    return () => context.revert()
  }, [isOpen, wizard.step])

  return (
    <Modal.Root isOpen={isOpen} onOpenChange={(next: boolean) => { if (!next) wizard.close() }}>
      <Modal.Backdrop variant="blur" isDismissable={wizard.phase !== 'busy'}>
        <Modal.Container size="lg" scroll="inside" className="sm:max-w-4xl">
          <Modal.Dialog>
            <Modal.Header className="relative items-center justify-center px-12 pt-5 text-center">
              <div className="min-w-0">
                <Modal.Heading className="text-xl font-semibold tracking-[-0.01em]">{t('addAccountTitle')}</Modal.Heading>
                <p className="mt-1 text-xs font-normal leading-5 text-muted">
                  {wizard.step === 'method' ? t('addAccountDesc') : (wizard.hint || t('addAccountDesc'))}
                </p>
              </div>
              <Modal.CloseTrigger
                aria-label={t('close')}
                className="absolute right-4 top-4 grid size-8 shrink-0 place-items-center rounded-lg text-muted transition-colors hover:bg-surface-secondary hover:text-foreground"
              >
                <X size={16} />
              </Modal.CloseTrigger>
            </Modal.Header>
            <Modal.Body className="px-6 pb-2">
              <div ref={stepBody}>
                {wizard.step === 'method' ? (
                  <AddAccountMethodStep
                    loading={wizard.typesLoading}
                    ready={wizard.typesReady}
                    options={wizard.providerOptions}
                    locked={wizard.settingsLocked}
                    selected={wizard.activeOption?.id}
                    onChoose={wizard.chooseProvider}
                    t={t}
                  />
                ) : (
                  <AddAccountLoginStep wizard={wizard} t={t} />
                )}
              </div>
            </Modal.Body>
            <Modal.Footer className="justify-end px-5 pb-5">
              <Button variant="ghost" onPress={wizard.close} isDisabled={wizard.phase === 'busy'}>{t('cancel')}</Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal.Root>
  )
}
