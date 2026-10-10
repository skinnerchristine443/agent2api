import { Button, Input, Label, Modal, TextArea, TextField } from '@heroui/react'
import { ArrowSquareOut, Key, ShieldCheck, X } from '@phosphor-icons/react'
import { ProviderMark } from '@/components/brand/ProviderMark'
import type { AccountRow } from '@/lib/account'
import { accountProviderLabel } from '@/lib/provider'
import type { Translate } from '@/i18n/messages'

type Props = {
  account: AccountRow | null
  busyKind: string
  t: Translate
  authUrl?: string
  note?: string
  pat: string
  onPatChange: (value: string) => void
  onDeviceLogin?: () => void
  onPatLogin?: () => void
  callbackUrl?: string
  onCallbackChange?: (value: string) => void
  onSubmitCallback?: () => void
  onClose: () => void
}

/**
 * 账号认证模态：自账号行「认证方式」列打开，承载设备登录 / PAT / 回调提交。
 * 原展开区被移除后，这里是唯一的重新登录 / 补认证入口（设计决策 2026-10-10）。
 */
export function AccountAuthModal({
  account,
  busyKind,
  t,
  authUrl,
  note,
  pat,
  onPatChange,
  onDeviceLogin,
  onPatLogin,
  callbackUrl,
  onCallbackChange,
  onSubmitCallback,
  onClose,
}: Props) {
  const provider = account ? accountProviderLabel(account.provider, account.region, t) : ''
  return (
    <Modal.Root isOpen={Boolean(account)} onOpenChange={(next: boolean) => { if (!next) onClose() }}>
      <Modal.Backdrop variant="blur">
        <Modal.Container placement="center" size="lg" scroll="inside">
          <Modal.Dialog className="sm:min-w-[32rem]">
            <Modal.Header className="items-start justify-between gap-4 px-6 pt-6">
              <div className="min-w-0">
                <Modal.Heading className="text-xl font-semibold tracking-[-0.015em]">{t('authentication')}</Modal.Heading>
                <p className="mt-1.5 text-sm font-normal leading-6 text-muted">{t('accountLoginHint')}</p>
                {account ? (
                  <div className="mt-3 flex flex-wrap items-center gap-2">
                    <span className="flex items-center gap-1.5">
                      <ProviderMark provider={account.provider} size={14} />
                      <span className="text-xs text-muted">{provider}</span>
                    </span>
                    <span className="mono text-xs text-muted">{account.name || account.id}</span>
                  </div>
                ) : null}
              </div>
              <Modal.CloseTrigger aria-label={t('close')} className="grid size-9 shrink-0 place-items-center rounded-lg text-muted hover:bg-surface-secondary">
                <X size={18} />
              </Modal.CloseTrigger>
            </Modal.Header>
            <Modal.Body className="space-y-4 px-6 pb-2 pt-1">
              {onDeviceLogin ? (
                <div>
                  <div className="text-xs font-medium text-secondary">{t('oauthDeviceFlow')}</div>
                  <div className="mt-2.5 flex flex-wrap gap-2">
                    <Button size="sm" isPending={busyKind === 'device'} onPress={onDeviceLogin}><ShieldCheck size={14} />{t('startBrowserLogin')}</Button>
                    {authUrl ? <Button size="sm" variant="ghost" onPress={() => window.open(authUrl, '_blank', 'noopener,noreferrer')}><ArrowSquareOut size={14} />{t('open')}</Button> : null}
                  </div>
                  {account?.provider === 'trae' && onSubmitCallback && onCallbackChange ? (
                    <div className="mt-3 space-y-2">
                      <p className="text-micro leading-4 text-secondary">{t('wizardCallbackLead')}</p>
                      <TextArea
                        className="h-24 w-full resize-none font-mono text-xs leading-5"
                        value={callbackUrl || ''}
                        onChange={(event) => onCallbackChange(event.target.value)}
                        placeholder={t('wizardCallbackPh')}
                        aria-label={t('wizardCallbackPh')}
                      />
                      <Button size="sm" variant="secondary" isPending={busyKind === 'callback'} onPress={onSubmitCallback}>
                        {t('wizardSubmitCallback')}
                      </Button>
                    </div>
                  ) : null}
                </div>
              ) : null}
              {onPatLogin ? (
                <div>
                  <div className="text-xs font-medium text-secondary">{t('patFallback')}</div>
                  <div className="mt-2.5 flex flex-col gap-2 sm:flex-row">
                    <TextField className="flex-1" type="password" value={pat} onChange={onPatChange}>
                      <Label className="sr-only">{t('pat')}</Label>
                      <Input placeholder={t('pasteToken')} aria-label={t('pat')} />
                    </TextField>
                    <Button size="sm" variant="secondary" isPending={busyKind === 'pat'} onPress={onPatLogin}><Key size={14} />{t('usePat')}</Button>
                  </div>
                </div>
              ) : null}
              {authUrl || note ? (
                <div className="text-xs">
                  {authUrl ? <code className="mono block break-all text-secondary">{authUrl}</code> : null}
                  {note ? <p className="mt-1 text-secondary">{note}</p> : null}
                </div>
              ) : null}
            </Modal.Body>
            <Modal.Footer className="justify-end gap-2 px-6 pb-6">
              <Button variant="ghost" onPress={onClose}>{t('close')}</Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal.Root>
  )
}
