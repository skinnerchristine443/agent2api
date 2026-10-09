import { Button, Input } from '@heroui/react'
import { CheckCircle, Key } from '@phosphor-icons/react'

import { FormRow } from '@/components/ui/FormRow'
import type { Translate } from '@/i18n/messages'

type Props = {
  isDone: boolean
  busy: boolean
  message: string
  pat: string
  onPatChange: (value: string) => void
  onSubmit: () => void
  t: Translate
}

/** PAT 登录页签：粘贴个人访问令牌并创建登录。 */
export function AddAccountPatTab({ isDone, busy, message, pat, onPatChange, onSubmit, t }: Props) {
  return (
    <>
      <FormRow label={t('tabPat')}>
        <Input
          type="password"
          value={pat}
          onChange={(event) => onPatChange(event.target.value)}
          placeholder={t('wizardPatPh')}
          aria-label={t('wizardPatPh')}
          disabled={busy}
        />
      </FormRow>
      {message ? (
        <p className="flex items-center gap-2 rounded-lg border border-separator bg-surface-secondary px-3 py-2 text-xs">
          {isDone ? <CheckCircle size={14} className="shrink-0 text-success" /> : null}
          <span className={isDone ? 'font-medium text-foreground' : 'text-muted'}>{message}</span>
        </p>
      ) : null}
      <Button className="w-full" isPending={busy} onPress={onSubmit}>
        {isDone
          ? <><CheckCircle size={15} />{t('patDone')}</>
          : <><Key size={15} />{t('wizardCreateAndLogin')}</>}
      </Button>
    </>
  )
}
