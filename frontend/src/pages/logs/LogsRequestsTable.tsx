import { Button, Chip, Table } from '@heroui/react'
import { MagnifyingGlass } from '@phosphor-icons/react'

import type { RequestLog } from '@/api/logs'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { SectionCard } from '@/components/ui/SectionCard'
import { LogsRequestListSkeleton } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'

import { TokenSplit } from './TokenSplit'
import { formatLatency, formatTime, reasoningLabel, statusColor } from '@/lib/logsFormat'

/**
 * 请求日志表格：加载中 = 列表骨架；空态区分「无记录」与「筛选后无结果」；
 * 行点击 = 打开详情 Sheet（写入 `?request=<id>`，由页面注入）。
 */
export function LogsRequestsTable({
  items,
  loading,
  hasFilters,
  onOpenRequest,
  onClearFilters,
  accountNameById,
  providerLabel,
}: {
  items: RequestLog[]
  loading: boolean
  hasFilters: boolean
  onOpenRequest: (id: string) => void
  onClearFilters: () => void
  accountNameById: Map<string, string>
  providerLabel: (item: { account_id?: string; provider?: string }) => string
}) {
  const { t } = useI18n()

  return (
    <SectionCard padded={false} className="overflow-hidden">
      {loading ? (
        <LogsRequestListSkeleton />
      ) : items.length === 0 ? (
        <EmptyPanel
          icon={<MagnifyingGlass size={22} />}
          title={hasFilters ? t('logsNoMatch') : t('logsEmptyRequests')}
          action={hasFilters ? <Button size="sm" variant="ghost" onPress={onClearFilters}>{t('clearFilters')}</Button> : null}
        />
      ) : (
        <Table>
          <Table.ScrollContainer>
            <Table.Content aria-label={t('logsRequests')}>
              <Table.Header>
                <Table.Column isRowHeader>{t('logsColTime')}</Table.Column>
                <Table.Column>{t('logsColModel')}</Table.Column>
                <Table.Column>{t('logsColReasoning')}</Table.Column>
                <Table.Column>{t('logsColProvider')}</Table.Column>
                <Table.Column>{t('logsColAccount')}</Table.Column>
                <Table.Column>{t('logsColStatus')}</Table.Column>
                <Table.Column>{t('logsColStream')}</Table.Column>
                <Table.Column>{t('logsColLatency')}</Table.Column>
                <Table.Column>{t('logsColTTFT')}</Table.Column>
                <Table.Column>{t('logsColTokens')}</Table.Column>
              </Table.Header>
              <Table.Body>
                {items.map((item) => (
                  <Table.Row key={item.id} className="cursor-pointer" onAction={() => onOpenRequest(item.id)}>
                    <Table.Cell>
                      <div className="py-1">
                        <div className="mono text-xs">{formatTime(item.created_at)}</div>
                        <div className="mono mt-0.5 text-micro text-muted">{item.id}</div>
                      </div>
                    </Table.Cell>
                    <Table.Cell>
                      <div className="text-sm font-medium">{item.requested_model || '—'}</div>
                      {item.mapped_model && item.mapped_model !== item.requested_model ? (
                        <div className="mono mt-0.5 text-micro text-muted">{item.mapped_model}</div>
                      ) : null}
                    </Table.Cell>
                    <Table.Cell>
                      <span className="mono text-xs">{reasoningLabel(item) || '—'}</span>
                    </Table.Cell>
                    <Table.Cell>
                      <span className="text-xs">{providerLabel(item)}</span>
                    </Table.Cell>
                    <Table.Cell>
                      <span className="text-xs">
                        {item.account_id ? (accountNameById.get(item.account_id) || item.account_id) : '—'}
                      </span>
                      {item.account_id && accountNameById.get(item.account_id) && accountNameById.get(item.account_id) !== item.account_id ? (
                        <div className="mono mt-0.5 text-micro text-muted">{item.account_id}</div>
                      ) : null}
                    </Table.Cell>
                    <Table.Cell>
                      <Chip size="sm" variant="soft" color={statusColor(item.status)}>{item.status}</Chip>
                      {item.error_kind ? <div className="mono mt-1 text-micro text-muted">{item.error_kind}</div> : null}
                    </Table.Cell>
                    <Table.Cell>
                      <span className="text-xs text-muted">{item.stream ? t('logsStreamYes') : t('logsStreamNo')}</span>
                    </Table.Cell>
                    <Table.Cell><span className="mono text-xs">{formatLatency(item.latency_ms)}</span></Table.Cell>
                    <Table.Cell><span className="mono text-xs">{formatLatency(item.ttfb_ms)}</span></Table.Cell>
                    <Table.Cell>
                      <TokenSplit
                        log={item}
                        inLabel={t('logsTokensIn')}
                        outLabel={t('logsTokensOut')}
                        pointsLabel={(value) => t('logsTokensPoints', { value })}
                      />
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table>
      )}
    </SectionCard>
  )
}
