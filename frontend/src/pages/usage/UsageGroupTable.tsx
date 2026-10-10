import type { UsageStatsGroup } from '@/api/usage'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { useI18n } from '@/hooks/I18nContext'
import { formatCompact } from '@/lib/format'

import { formatSpeed } from './usageFormat'

/** 分组聚合表（按账号 / 按模型两张表共用同一形态）。 */
export function UsageGroupTable({
  title,
  hint,
  empty,
  groups,
  nameOf,
}: {
  title: string
  hint: string
  empty: string
  groups: UsageStatsGroup[]
  /** 分组键 → 展示名（仅「按账号」表需要：后端给的是账号 id，须映射成账号名）。 */
  nameOf?: (key: string) => string
}) {
  const { t } = useI18n()
  const label = (key: string) => {
    if (key === '(unknown)') return t('statsUnknown')
    if (key === '(unassigned)') return t('statsUnassigned')
    return nameOf?.(key) ?? key
  }
  return (
    <section className="overflow-hidden rounded-2xl border border-border bg-surface">
      <div className="border-b border-separator px-4 py-3">
        <div className="text-sm font-semibold">{title}</div>
        <div className="mt-0.5 text-xs text-muted">{hint}</div>
      </div>
      {groups.length === 0 ? (
        <EmptyPanel title={empty} className="min-h-32" />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="text-micro font-medium tracking-[0.08em] text-muted uppercase">
                <th className="px-4 py-2 text-left font-medium">{title}</th>
                <th className="px-3 py-2 text-right font-medium">{t('usageColRequests')}</th>
                <th className="px-3 py-2 text-right font-medium">{t('usageColPrompt')}</th>
                <th className="px-3 py-2 text-right font-medium">{t('usageColCompletion')}</th>
                <th className="px-3 py-2 text-right font-medium">{t('usageColTotal')}</th>
                <th className="px-3 py-2 text-right font-medium">{t('usageColSpeed')}</th>
                <th className="px-4 py-2 text-right font-medium">{t('usageColErrors')}</th>
              </tr>
            </thead>
            <tbody>
              {groups.map((group) => (
                <tr key={group.key} className="border-t border-separator">
                  <td className="max-w-[240px] truncate px-4 py-2 font-medium" title={group.key}>{label(group.key)}</td>
                  <td className="mono px-3 py-2 text-right text-muted">{formatCompact(group.requests)}</td>
                  <td className="mono px-3 py-2 text-right text-muted">{formatCompact(group.prompt_tokens)}</td>
                  <td className="mono px-3 py-2 text-right text-muted">{formatCompact(group.completion_tokens)}</td>
                  <td className="mono px-3 py-2 text-right font-medium">{formatCompact(group.total_tokens)}</td>
                  <td className="mono px-3 py-2 text-right text-muted">{formatSpeed(group.output_tokens_per_second)}</td>
                  <td className="mono px-4 py-2 text-right text-muted">{group.errors > 0 ? group.errors : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
