import { Input } from '@heroui/react'

import { FormRow } from '@/components/ui/FormRow'
import type { Translate } from '@/i18n/messages'

type Props = {
  name: string
  onNameChange: (value: string) => void
  locked: boolean
  t: Translate
}

/**
 * 添加向导的账号设置：只留账号名。
 *
 * 运行参数（最大并发 / 优先级 / 代理 / 丢弃系统提示词 / 四道日限额）已迁到
 * 「设置 › 账号默认（按渠道）」，新账号一律取渠道默认，因此这里不再逐账号询问
 * （设计决策 2026-10-10）。
 */
export function AddAccountSettings({ name, onNameChange, locked, t }: Props) {
  return (
    <div className="mt-4 space-y-3">
      <FormRow label={t('accountName')}>
        <Input
          value={name}
          onChange={(event) => onNameChange(event.target.value)}
          placeholder={t('wizardNamePh')}
          aria-label={t('accountName')}
          disabled={locked}
          autoFocus
        />
      </FormRow>
    </div>
  )
}
