import { Button, Chip } from '@heroui/react'
import { Gift } from '@phosphor-icons/react'

import type { GrowthClaimResult } from '@/api/growth'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import type { Translate } from '@/i18n/messages'
import { formatCompact } from '@/lib/format'

import { GrowthRewards } from './GrowthPanel'
import { claimedTotals, outcomeLabelKey, outcomeTone } from './growthModel'

type Props = {
  result: GrowthClaimResult | null
  pending: boolean
  error: string | null
  onClaim: () => void
  t: Translate
}

/**
 * 幂等领取区：按钮 + 结果分区展示——`outcomes` 逐项（success / already_claimed /
 * skipped / failed + credit / energy）与 `errors[]`（无法尝试的区块级原因）。
 * already_claimed 与 skipped 都以「成功/无需操作」呈现，不误报失败。
 */
export function GrowthClaimSection({ result, pending, error, onClaim, t }: Props) {
  const totals = result ? claimedTotals(result.outcomes || []) : null
  const outcomes = result?.outcomes || []
  const errors = result?.errors || []

  return (
    <SectionCard
      title={t('growthClaim')}
      hint={t('growthClaimHint')}
      right={(
        <Button size="sm" isPending={pending} onPress={onClaim}>
          <Gift size={15} />{t('growthClaimNow')}
        </Button>
      )}
    >
      {error ? <PageAlert title={error} /> : null}

      {!result && !error ? (
        <p className="text-xs text-muted">{t('growthClaimIdle')}</p>
      ) : null}

      {result ? (
        <div className="space-y-3">
          <p className="text-xs text-muted">
            {totals && totals.claimed > 0
              ? t('growthClaimTotals', {
                  count: totals.claimed,
                  credit: formatCompact(totals.credit),
                  energy: formatCompact(totals.energy),
                })
              : t('growthClaimEmpty')}
          </p>

          {outcomes.length ? (
            <ul className="space-y-2">
              {outcomes.map((outcome, index) => (
                <li
                  key={`${outcome.target}-${outcome.action}-${index}`}
                  className="flex flex-wrap items-center justify-between gap-2 border-t border-separator pt-2 first:border-t-0 first:pt-0"
                >
                  <div className="flex min-w-0 items-center gap-2">
                    <Chip size="sm" variant="soft" color={outcomeTone(outcome.status)}>
                      {t(outcomeLabelKey(outcome.status))}
                    </Chip>
                    <span className="truncate text-xs font-medium" title={outcome.target}>{outcome.target}</span>
                    <span className="mono text-micro text-muted">{outcome.action}</span>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <GrowthRewards credit={outcome.credit} energy={outcome.energy} t={t} />
                    {outcome.message ? <span className="max-w-72 truncate text-micro text-muted" title={outcome.message}>{outcome.message}</span> : null}
                  </div>
                </li>
              ))}
            </ul>
          ) : null}

          {errors.length ? (
            <div className="rounded-lg border border-warning/30 bg-warning/5 px-3 py-2">
              <div className="text-xs font-medium text-warning">{t('growthClaimErrors')}</div>
              <ul className="mt-1 space-y-0.5 text-xs leading-5 text-muted">
                {errors.map((item) => <li key={item} className="break-words">{item}</li>)}
              </ul>
            </div>
          ) : null}
        </div>
      ) : null}
    </SectionCard>
  )
}
