import { Skeleton } from '@heroui/react'

import { ProviderMark } from '@/components/brand/ProviderMark'
import { OptionTiles } from '@/components/ui/OptionTiles'
import type { Translate } from '@/i18n/messages'

import { optionHint, optionLabel, type ProviderOption } from './providerOptions'

type Props = {
  loading: boolean
  ready: boolean
  options: ProviderOption[]
  locked: boolean
  /** 预选渠道（批次 9：页签联动，置顶 + 高亮；'' = 无预选）。 */
  selected?: string
  onChoose: (id: string) => void
  t: Translate
}

function AccountTypeSkeleton({ ariaLabel }: { ariaLabel: string }) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2" aria-busy="true" aria-label={ariaLabel}>
      {Array.from({ length: 6 }, (_, index) => (
        <div key={index} className="flex items-center gap-2.5 rounded-xl border border-separator px-3 py-2.5">
          <Skeleton className="size-7 shrink-0 rounded-lg" />
          <Skeleton className="h-4 w-20 rounded-lg" />
        </div>
      ))}
    </div>
  )
}

/** 添加向导第一步：选择账号类型（provider × region；预选渠道置顶高亮）。 */
export function AddAccountMethodStep({ loading, ready, options, locked, selected, onChoose, t }: Props) {
  if (loading) return <AccountTypeSkeleton ariaLabel={t('accountType')} />
  if (!ready) {
    return (
      <p className="rounded-lg border border-separator bg-surface-secondary/45 px-3.5 py-3 text-xs leading-5 text-muted">
        {t('accountTypeHint')}
      </p>
    )
  }
  return (
    <section className="space-y-2.5">
      <OptionTiles
        ariaLabel={t('accountType')}
        columns={2}
        value={selected ?? ''}
        onChange={onChoose}
        options={options.map((option) => ({
          value: option.id,
          label: optionLabel(option, t),
          hint: optionHint(option, t),
          icon: <ProviderMark provider={option.provider} size={18} />,
          disabled: locked,
        }))}
      />
    </section>
  )
}
