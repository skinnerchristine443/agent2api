import type { UsageStatsModelAccount } from '@/api/usage'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { SectionCard } from '@/components/ui/SectionCard'
import { TableSection, TableSectionCell, TableSectionRow } from '@/components/ui/TableSection'
import { useI18n } from '@/hooks/I18nContext'
import { formatCompact } from '@/lib/format'

import { formatSpeed, groupLabel } from './usageFormat'

/** 模型 × 账号汇总表：谁承载了哪个模型（按用量排序，Top 20）。 */
export function UsageModelAccountTable({
  title,
  hint,
  empty,
  rows,
  nameOf,
}: {
  title: string
  hint: string
  empty: string
  rows: UsageStatsModelAccount[]
  /** 账号 id → 展示名（后端给的是账号内部 id，须映射成账号名）。 */
  nameOf: (id: string) => string
}) {
  const { t } = useI18n()
  const label = (key: string) => groupLabel(key, { unknown: t('statsUnknown'), unassigned: t('statsUnassigned') })
  // 账号列：哨兵值先本地化，其余经 id→名称映射（后端给的是账号内部 id）。
  const accountLabel = (id: string) => (
    id === '(unknown)' || id === '(unassigned)' ? label(id) : nameOf(id)
  )
  return (
    <SectionCard title={title} hint={hint} padded={false}>
      {rows.length === 0 ? (
        <div className="p-4">
          <EmptyPanel title={empty} size="sm" />
        </div>
      ) : (
        <TableSection
          minWidthClass="min-w-[560px]"
          head={[t('usageColModel'), t('usageColAccount'), t('usageColRequests'), t('usageColTotal'), t('usageColSpeed')]}
        >
          {rows.map((row) => (
            <TableSectionRow key={`${row.model}\u0000${row.account}`}>
              <TableSectionCell className="max-w-[200px] truncate font-medium" title={row.model}>{label(row.model)}</TableSectionCell>
              <TableSectionCell className="max-w-[160px] truncate text-muted" title={accountLabel(row.account)}>{accountLabel(row.account)}</TableSectionCell>
              <TableSectionCell className="mono text-right text-muted">{formatCompact(row.requests)}</TableSectionCell>
              <TableSectionCell className="mono text-right font-medium">{formatCompact(row.total_tokens)}</TableSectionCell>
              <TableSectionCell className="mono text-right text-muted">{formatSpeed(row.output_tokens_per_second)}</TableSectionCell>
            </TableSectionRow>
          ))}
        </TableSection>
      )}
    </SectionCard>
  )
}
