import { useState } from 'react'
import { Alert, Button, Chip, Form, Input, Modal, NumberField } from '@heroui/react'
import { X } from '@phosphor-icons/react'
import { ProviderMark } from '@/components/brand/ProviderMark'
import { FormRow } from '@/components/ui/FormRow'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import type { AccountRow } from '@/lib/account'
import { accountProviderLabel } from '@/lib/provider'
import type { Translate } from '@/i18n/messages'


type Props = {
  account: AccountRow | null
  busy: boolean
  t: Translate
  onClose: () => void
  onSave: (input: { name: string; max_inflight: number; priority: number; proxy_url: string; drop_system_prompt?: boolean; model_requests_enabled?: boolean; reserve_credits?: number; daily_token_limit?: number; daily_credit_limit?: number; daily_model_token_limit?: number }) => Promise<void>
}

export function EditAccountModal({ account, busy, t, onClose, onSave }: Props) {
  const [name, setName] = useState(account?.name || '')
  const [maxInFlight, setMaxInFlight] = useState<number>(account?.max_inflight ?? 4)
  const [priority, setPriority] = useState<number>(account?.priority ?? 50)
  const [proxyUrl, setProxyUrl] = useState(account?.proxy_url || '')
  const [dropSystemPrompt, setDropSystemPrompt] = useState(Boolean(account?.drop_system_prompt))
  const [modelRequests, setModelRequests] = useState(account?.model_requests_enabled !== false)
  const [reserveCredits, setReserveCredits] = useState<number>(account?.reserve_credits ?? 0)
  const [dailyTokenLimit, setDailyTokenLimit] = useState<number>(account?.daily_token_limit ?? 0)
  const [dailyCreditLimit, setDailyCreditLimit] = useState<number>(account?.daily_credit_limit ?? 0)
  const [dailyModelTokenLimit, setDailyModelTokenLimit] = useState<number>(account?.daily_model_token_limit ?? 0)
  const [error, setError] = useState('')
  const title = t('editAccountTitle', { name: account?.name || account?.id || '' })
  const provider = account ? accountProviderLabel(account.provider, account.region, t) : ''

  async function submit(event?: { preventDefault(): void }) {
    event?.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) {
      setError(t('accountNameRequired'))
      return
    }
    if (!Number.isInteger(maxInFlight) || maxInFlight < 1 || maxInFlight > 32) {
      setError(t('maxInflightInvalid'))
      return
    }
    if (!Number.isInteger(priority) || priority < 1 || priority > 100) {
      setError(t('priorityInvalid'))
      return
    }
    setError('')
    const guardValue = (value: number) => (Number.isFinite(value) ? Math.max(0, Math.trunc(value)) : 0)
    try {
      await onSave({
        name: trimmed,
        max_inflight: maxInFlight,
        priority,
        proxy_url: proxyUrl.trim(),
        drop_system_prompt: account?.provider === 'workbuddy' ? dropSystemPrompt : undefined,
        model_requests_enabled: modelRequests,
        reserve_credits: guardValue(reserveCredits),
        daily_token_limit: guardValue(dailyTokenLimit),
        daily_credit_limit: guardValue(dailyCreditLimit),
        daily_model_token_limit: guardValue(dailyModelTokenLimit),
      })
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Modal.Root isOpen={Boolean(account)} onOpenChange={(next: boolean) => { if (!next && !busy) onClose() }}>
      <Modal.Backdrop variant="blur">
        <Modal.Container placement="center" size="lg" scroll="inside">
          <Modal.Dialog className="sm:min-w-[32rem]">
            <Modal.Header className="items-start justify-between gap-4 px-6 pt-6">
              <div className="min-w-0">
                <Modal.Heading className="text-xl font-semibold tracking-[-0.015em]">{title}</Modal.Heading>
                <p className="mt-1.5 text-sm font-normal leading-6 text-muted">{t('editAccountHint')}</p>
                {account ? (
                  <div className="mt-3 flex flex-wrap items-center gap-2">
                    <Chip size="sm" variant="soft">
                      <span className="flex items-center gap-1.5">
                        <ProviderMark provider={account.provider} size={14} />
                        <span>{provider}</span>
                      </span>
                    </Chip>
                    <span className="mono text-xs text-muted">{account.id}</span>
                  </div>
                ) : null}
              </div>
              <Modal.CloseTrigger isDisabled={busy} aria-label={t('close')} className="grid size-9 shrink-0 place-items-center rounded-lg text-muted hover:bg-surface-secondary">
                <X size={18} />
              </Modal.CloseTrigger>
            </Modal.Header>
            <Modal.Body className="px-6 pb-2 pt-1">
              {error ? (
                <Alert status="danger" className="mb-4">
                  <Alert.Indicator />
                  <Alert.Content>
                    <Alert.Title>{error}</Alert.Title>
                  </Alert.Content>
                </Alert>
              ) : null}
              <Form className="space-y-4" onSubmit={(event) => void submit(event)}>
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
                <FormRow label={t('maxInflight')} hint={t('maxInflightHint')}>
                  <NumberField
                    value={maxInFlight}
                    onChange={(value) => setMaxInFlight(value ?? 4)}
                    minValue={1}
                    maxValue={32}
                    isDisabled={busy}
                    isRequired
                    aria-label={t('maxInflight')}
                  >
                    <NumberField.Group>
                      <NumberField.DecrementButton />
                      <NumberField.Input />
                      <NumberField.IncrementButton />
                    </NumberField.Group>
                  </NumberField>
                </FormRow>
                <FormRow label={t('priority')} hint={t('priorityHint')}>
                  <NumberField
                    value={priority}
                    onChange={(value) => setPriority(value ?? 50)}
                    minValue={1}
                    maxValue={100}
                    isDisabled={busy}
                    isRequired
                    aria-label={t('priority')}
                  >
                    <NumberField.Group>
                      <NumberField.DecrementButton />
                      <NumberField.Input />
                      <NumberField.IncrementButton />
                    </NumberField.Group>
                  </NumberField>
                </FormRow>
                <FormRow label={t('proxyUrl')} hint={t('proxyUrlHint')}>
                  <Input
                    value={proxyUrl}
                    onChange={(event) => setProxyUrl(event.target.value)}
                    placeholder={t('proxyUrlPlaceholder')}
                    aria-label={t('proxyUrl')}
                    disabled={busy}
                  />
                </FormRow>
                {account?.provider === 'workbuddy' ? (
                  <FormRow label={t('dropSystemPrompt')} hint={t('dropSystemPromptHint')}>
                    <CompactSwitch
                      isSelected={dropSystemPrompt}
                      isDisabled={busy}
                      ariaLabel={t('dropSystemPrompt')}
                      onChange={setDropSystemPrompt}
                    />
                  </FormRow>
                ) : null}
                <FormRow label={t('modelRequests')} hint={t('modelRequestsHint')}>
                  <CompactSwitch
                    isSelected={modelRequests}
                    isDisabled={busy}
                    ariaLabel={t('modelRequests')}
                    label={modelRequests ? t('modelRequestsOn') : t('modelRequestsOff')}
                    onChange={setModelRequests}
                  />
                </FormRow>
                <FormRow label={t('dailyGuardReserve')} hint={t('dailyGuardReserveHint')}>
                  <NumberField
                    value={reserveCredits}
                    onChange={(value) => setReserveCredits(value ?? 0)}
                    minValue={0}
                    isDisabled={busy}
                    aria-label={t('dailyGuardReserve')}
                  >
                    <NumberField.Group>
                      <NumberField.DecrementButton />
                      <NumberField.Input />
                      <NumberField.IncrementButton />
                    </NumberField.Group>
                  </NumberField>
                </FormRow>
                <FormRow label={t('dailyGuardTokenLimit')} hint={t('dailyGuardTokenLimitHint')}>
                  <NumberField
                    value={dailyTokenLimit}
                    onChange={(value) => setDailyTokenLimit(value ?? 0)}
                    minValue={0}
                    isDisabled={busy}
                    aria-label={t('dailyGuardTokenLimit')}
                  >
                    <NumberField.Group>
                      <NumberField.DecrementButton />
                      <NumberField.Input />
                      <NumberField.IncrementButton />
                    </NumberField.Group>
                  </NumberField>
                </FormRow>
                <FormRow label={t('dailyGuardCreditLimit')} hint={t('dailyGuardCreditLimitHint')}>
                  <NumberField
                    value={dailyCreditLimit}
                    onChange={(value) => setDailyCreditLimit(value ?? 0)}
                    minValue={0}
                    isDisabled={busy}
                    aria-label={t('dailyGuardCreditLimit')}
                  >
                    <NumberField.Group>
                      <NumberField.DecrementButton />
                      <NumberField.Input />
                      <NumberField.IncrementButton />
                    </NumberField.Group>
                  </NumberField>
                </FormRow>
                <FormRow label={t('dailyGuardModelTokenLimit')} hint={t('dailyGuardModelTokenLimitHint')}>
                  <NumberField
                    value={dailyModelTokenLimit}
                    onChange={(value) => setDailyModelTokenLimit(value ?? 0)}
                    minValue={0}
                    isDisabled={busy}
                    aria-label={t('dailyGuardModelTokenLimit')}
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
