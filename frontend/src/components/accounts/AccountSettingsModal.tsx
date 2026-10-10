import { useState } from 'react'
import { Button, Modal, NumberField, Form } from '@heroui/react'
import { X } from '@phosphor-icons/react'
import { FormRow } from '@/components/ui/FormRow'
import type { AccountRow } from '@/lib/account'
import type { Translate } from '@/i18n/messages'

type Props = {
  account: AccountRow | null
  busy: boolean
  t: Translate
  onClose: () => void
  onSave: (priority: number) => Promise<void>
}

/**
 * 账号设置（自 ⋯ 更多操作菜单打开）：优先级（1–100，账号级）。
 * 列表不再内联编辑优先级 —— 列过挤，编辑入口收敛到本模态。
 */
export function AccountSettingsModal({ account, busy, t, onClose, onSave }: Props) {
  const [priority, setPriority] = useState(() => (Number.isFinite(account?.priority) ? Number(account?.priority) : 50))
  const [error, setError] = useState('')

  async function submit(event?: { preventDefault(): void }) {
    event?.preventDefault()
    setError('')
    try {
      await onSave(priority)
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Modal.Root isOpen={Boolean(account)} onOpenChange={(next: boolean) => { if (!next && !busy) onClose() }}>
      <Modal.Backdrop variant="blur">
        <Modal.Container placement="center" size="sm">
          <Modal.Dialog className="sm:min-w-[24rem]">
            <Modal.Header className="items-start justify-between gap-4 px-6 pt-6">
              <Modal.Heading className="text-xl font-semibold tracking-[-0.01em]">{t('accountSettings')}</Modal.Heading>
              <Modal.CloseTrigger isDisabled={busy} aria-label={t('close')} className="grid size-9 shrink-0 place-items-center rounded-lg text-muted hover:bg-surface-secondary">
                <X size={18} />
              </Modal.CloseTrigger>
            </Modal.Header>
            <Modal.Body className="px-6 pb-2 pt-1">
              {error ? <p className="mb-3 text-xs text-danger">{error}</p> : null}
              <Form onSubmit={(event) => void submit(event)}>
                <FormRow label={t('priority')}>
                  <NumberField
                    value={priority}
                    onChange={(value) => setPriority(value ?? priority)}
                    minValue={1}
                    maxValue={100}
                    isDisabled={busy}
                    aria-label={t('priority')}
                  >
                    <NumberField.Group>
                      <NumberField.DecrementButton />
                      <NumberField.Input />
                      <NumberField.IncrementButton />
                    </NumberField.Group>
                  </NumberField>
                </FormRow>
              </Form>
            </Modal.Body>
            <Modal.Footer className="justify-end gap-2 px-6 pb-6">
              <Button variant="ghost" isDisabled={busy} onPress={onClose}>{t('cancel')}</Button>
              <Button isPending={busy} onPress={() => void submit()}>{t('save')}</Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal.Root>
  )
}
