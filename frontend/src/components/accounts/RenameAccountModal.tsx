import { useState } from 'react'
import { Button, Input, Modal, Form } from '@heroui/react'
import { X } from '@phosphor-icons/react'
import { FormRow } from '@/components/ui/FormRow'
import type { AccountRow } from '@/lib/account'
import type { Translate } from '@/i18n/messages'

type Props = {
  account: AccountRow | null
  busy: boolean
  t: Translate
  onClose: () => void
  onSave: (name: string) => Promise<void>
}

/**
 * 账号重命名（自 ⋯ 菜单打开）。编辑弹窗移除后，这里是唯一的改名入口。
 */
export function RenameAccountModal({ account, busy, t, onClose, onSave }: Props) {
  const [name, setName] = useState(account?.name || '')
  const [error, setError] = useState('')

  async function submit(event?: { preventDefault(): void }) {
    event?.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) {
      setError(t('accountNameRequired'))
      return
    }
    setError('')
    try {
      await onSave(trimmed)
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
              <Modal.Heading className="text-xl font-semibold tracking-[-0.01em]">{t('renameAccount')}</Modal.Heading>
              <Modal.CloseTrigger isDisabled={busy} aria-label={t('close')} className="grid size-9 shrink-0 place-items-center rounded-lg text-muted hover:bg-surface-secondary">
                <X size={18} />
              </Modal.CloseTrigger>
            </Modal.Header>
            <Modal.Body className="px-6 pb-2 pt-1">
              {error ? <p className="mb-3 text-xs text-danger">{error}</p> : null}
              <Form onSubmit={(event) => void submit(event)}>
                <FormRow label={t('accountName')}>
                  <Input
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    placeholder={t('wizardNamePh')}
                    aria-label={t('accountName')}
                    disabled={busy}
                    autoFocus
                  />
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
