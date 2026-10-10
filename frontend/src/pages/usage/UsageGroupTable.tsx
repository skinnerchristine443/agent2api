import type { UsageStatsGroup } from '@/api/usage'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { SectionCard } from '@/components/ui/SectionCard'
import { TableSection, TableSectionCell, TableSectionRow } from '@/components/ui/TableSection'
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
    <SectionCard title={title} hint={hint} padded={false}>
      {groups.length === 0 ? (
        <div className="p-4">
          <EmptyPanel title={empty} size="sm" />
        </div>
      ) : (
        <TableSection
          minWidthClass="min-w-[640px]"
          head={[
            title,
            t('usageColRequests'),
            t('usageColPrompt'),
            t('usageColCompletion'),
            t('usageColTotal'),
            t('usageColSpeed'),
            t('usageColErrors'),
          ]}
        >
          {groups.map((group) => (
            <TableSectionRow key={group.key}>
              <TableSectionCell className="max-w-[240px] truncate font-medium" title={group.key}>{label(group.key)}</TableSectionCell>
              <TableSectionCell className="mono text-right text-muted">{formatCompact(group.requests)}</TableSectionCell>
              <TableSectionCell className="mono text-right text-muted">{formatCompact(group.prompt_tokens)}</TableSectionCell>
              <TableSectionCell className="mono text-right text-muted">{formatCompact(group.completion_tokens)}</TableSectionCell>
              <TableSectionCell className="mono text-right font-medium">{formatCompact(group.total_tokens)}</TableSectionCell>
              <TableSectionCell className="mono text-right text-muted">{formatSpeed(group.output_tokens_per_second)}</TableSectionCell>
              <TableSectionCell className="mono text-right text-muted">{group.errors > 0 ? group.errors : '—'}</TableSectionCell>
            </TableSectionRow>
          ))}
        </TableSection>
      )}
    </SectionCard>
  )
}
