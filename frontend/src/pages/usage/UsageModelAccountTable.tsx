import type { UsageStatsModelAccount } from '@/api/usage'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { useI18n } from '@/hooks/I18nContext'
import { formatCompact } from '@/lib/format'

import { formatSpeed, groupLabel } from './usageFormat'

/** 模型 × 账号汇总表：谁承载了哪个模型（按用量排序，Top 20）。 */
export function UsageModelAccountTable({
  title,
  hint,
  empty,
  rows,
}: {
  title: string
  hint: string
  empty: string
  rows: UsageStatsModelAccount[]
}) {
  const { t } = useI18n()
  const label = (key: string) => groupLabel(key, { unknown: t('statsUnknown'), unassigned: t('statsUnassigned') })
  return (
    <section className="overflow-hidden rounded-2xl border border-border bg-surface">
      <div className="border-b border-separator px-4 py-3">
        <div className="text-sm font-semibold">{title}</div>
        <div className="mt-0.5 text-xs text-muted">{hint}</div>
      </div>
      {rows.length === 0 ? (
        <EmptyPanel title={empty} className="min-h-32" />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="text-micro font-medium tracking-[0.08em] text-muted uppercase">
                <th className="px-4 py-2 text-left font-medium">{t('usageColModel')}</th>
                <th className="px-3 py-2 text-left font-medium">{t('usageColAccount')}</th>
                <th className="px-3 py-2 text-right font-medium">{t('usageColRequests')}</th>
                <th className="px-3 py-2 text-right font-medium">{t('usageColTotal')}</th>
                <th className="px-4 py-2 text-right font-medium">{t('usageColSpeed')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={`${row.model}\u0000${row.account}`} className="border-t border-separator">
                  <td className="max-w-[200px] truncate px-4 py-2 font-medium" title={row.model}>{label(row.model)}</td>
                  <td className="max-w-[160px] truncate px-3 py-2 text-muted" title={row.account}>{label(row.account)}</td>
                  <td className="mono px-3 py-2 text-right text-muted">{formatCompact(row.requests)}</td>
                  <td className="mono px-3 py-2 text-right font-medium">{formatCompact(row.total_tokens)}</td>
                  <td className="mono px-4 py-2 text-right text-muted">{formatSpeed(row.output_tokens_per_second)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
