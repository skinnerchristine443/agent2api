import { Button, TextArea } from '@heroui/react'
import { ArrowSquareOut, CheckCircle, ShieldCheck } from '@phosphor-icons/react'

import { BrandMark } from '@/components/brand/BrandMark'
import type { Translate } from '@/i18n/messages'

import type { AddAccountPhase } from './types'

type Props = {
  phase: AddAccountPhase
  busy: boolean
  isDone: boolean
  message: string
  authUrl: string
  callbackUrl: string
  showCallbackPaste: boolean
  onCallbackChange: (value: string) => void
  onSubmitCallback: () => void
  onStart: () => void
  t: Translate
}

function StatusIcon({ phase, busy }: { phase: AddAccountPhase; busy: boolean }) {
  if (phase === 'done') return <CheckCircle size={16} className="text-success" />
  if (busy) return <BrandMark size={16} loading />
  return null
}

/** 浏览器（设备码）登录页签：授权链接 + 状态 + 回调粘贴（Trae）+ 主按钮。 */
export function AddAccountBrowserTab({
  phase,
  busy,
  isDone,
  message,
  authUrl,
  callbackUrl,
  showCallbackPaste,
  onCallbackChange,
  onSubmitCallback,
  onStart,
  t,
}: Props) {
  return (
    <>
      {authUrl ? (
        <div className="rounded-lg border border-separator bg-surface-secondary px-3 py-2.5">
          <div className="flex items-center gap-2 text-xs">
            <StatusIcon phase={phase} busy={busy} />
            <span className="text-muted">{message || t('loginOpenMsg')}</span>
          </div>
          <button
            onClick={() => window.open(authUrl, '_blank', 'noopener,noreferrer')}
            className="mt-2 inline-flex items-center gap-1.5 text-xs font-medium text-foreground hover:underline"
          >
            <ArrowSquareOut size={12} />{t('wizardOpenBrowser')}
          </button>
        </div>
      ) : null}
      {message && !authUrl ? (
        <p className="flex items-center gap-2 rounded-lg border border-separator bg-surface-secondary px-3 py-2 text-xs">
          {isDone ? <CheckCircle size={14} className="shrink-0 text-success" /> : null}
          <span className={isDone ? 'font-medium text-foreground' : 'text-muted'}>{message}</span>
        </p>
      ) : null}
      {showCallbackPaste ? (
        <div className="space-y-2">
          <p className="text-micro leading-4 text-muted">{t('wizardCallbackLead')}</p>
          <TextArea
            className="h-28 w-full resize-none font-mono text-xs leading-5"
            value={callbackUrl}
            onChange={(event) => onCallbackChange(event.target.value)}
            placeholder={t('wizardCallbackPh')}
            aria-label={t('wizardCallbackPh')}
            disabled={isDone}
          />
          <Button
            className="w-full"
            variant="secondary"
            isPending={busy && Boolean(callbackUrl.trim())}
            onPress={onSubmitCallback}
            isDisabled={isDone}
          >
            {t('wizardSubmitCallback')}
          </Button>
        </div>
      ) : null}
      <Button className="w-full" isPending={busy} onPress={onStart}>
        {isDone
          ? <><CheckCircle size={15} />{t('wizardAccountReady')}</>
          : <><ShieldCheck size={15} />{t('wizardStartBrowser')}</>}
      </Button>
    </>
  )
}
