import { Chip } from '@heroui/react'

import type { RequestLog } from '@/api/logs'
import { PageAlert } from '@/components/ui/PageAlert'
import { Sheet } from '@/components/ui/Sheet'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useApiQuery } from '@/hooks/useApiQuery'
import { useI18n } from '@/hooks/I18nContext'
import { fetchRequestLogDetail } from '@/hooks/useLogsQueries'
import { logsRoutingKey } from '@/i18n/messages'
import { formatCredit, formatLatency, reasoningLabel, statusColor } from '@/lib/logsFormat'

import { TokenSplit } from './TokenSplit'

function RequestDetailSkeleton() {
  return (
    <div className="space-y-5" aria-busy="true" aria-live="polite">
      <div className="grid gap-4 sm:grid-cols-2">
        {Array.from({ length: 8 }, (_, index) => (
          <div key={index} className="space-y-2">
            <SkeletonBlock className="h-3 w-20" />
            <SkeletonBlock className="h-5 w-36 max-w-full" />
          </div>
        ))}
      </div>
      <div className="space-y-3 rounded-lg bg-surface-secondary px-3 py-3">
        <SkeletonBlock className="h-3 w-24" />
        <SkeletonBlock className="h-4 w-full" />
        <SkeletonBlock className="h-4 w-4/5" />
      </div>
      <div className="space-y-3">
        <SkeletonBlock className="h-3 w-20" />
        <SkeletonBlock className="h-16 w-full" />
      </div>
    </div>
  )
}

/**
 * 请求详情 Sheet（`?request=<id>` 寻址）：尝试记录 / 流诊断 / 用量明细。
 *
 * 取数以 URL 里的 id 为准（**打开只取一次**，不依赖列表行对象），因此直链
 * 可分享、可换设备打开；错误就地呈现（详情接口 404 等不影响列表）。
 */
export function RequestDetailSheet({
  requestId,
  isOpen,
  onOpenChange,
  accountNameById,
  providerLabel,
}: {
  requestId: string | null
  isOpen: boolean
  onOpenChange: (isOpen: boolean) => void
  accountNameById: Map<string, string>
  providerLabel: (item: { account_id?: string; provider?: string }) => string
}) {
  const { t } = useI18n()
  const query = useApiQuery(
    (signal) => (requestId ? fetchRequestLogDetail(requestId, signal) : Promise.resolve(null)),
    `logs:request-detail\u0000${requestId ?? ''}`,
    { enabled: Boolean(requestId) },
  )
  const selected = query.data
  const routing = selected?.routing
  const routingText = routing ? t(logsRoutingKey(routing) ?? 'logsRouting_pool') : '—'

  return (
    <Sheet
      isOpen={isOpen}
      onOpenChange={onOpenChange}
      closeLabel={t('close')}
      className="w-full max-w-2xl"
      title={(
        <span className="flex items-baseline gap-2">
          <span>{t('logsDetailTitle')}</span>
          {requestId ? <span className="mono text-micro text-muted">{requestId}</span> : null}
        </span>
      )}
    >
      {query.loading ? (
        <RequestDetailSkeleton />
      ) : query.error ? (
        <PageAlert title={t('requestFailed')} description={query.error} />
      ) : selected ? (
        <div className="space-y-5">
          <dl className="grid gap-3 sm:grid-cols-2">
            {[
              [t('logsColStatus'), selected.status || '—'],
              [t('logsColModel'), selected.requested_model || '—'],
              [t('logsColReasoning'), reasoningLabel(selected) || '—'],
              [t('logsColProvider'), providerLabel(selected)],
              [t('logsColAccount'), selected.account_id ? (accountNameById.get(selected.account_id) || selected.account_id) : '—'],
              [t('logsColLatency'), formatLatency(selected.latency_ms)],
              [t('logsColTTFB'), formatLatency(selected.ttfb_ms)],
              [t('logsColTTFT'), selected.ttft_ms != null ? formatLatency(selected.ttft_ms) : '—'],
              [t('logsColStream'), selected.stream ? t('logsStreamYes') : t('logsStreamNo')],
              [t('logsRouting'), routingText],
            ].map(([label, value]) => (
              <div key={String(label)}>
                <dt className="text-micro text-muted">{label}</dt>
                <dd className="mt-1 break-all text-sm font-medium">{value}</dd>
              </div>
            ))}
            <div>
              <dt className="text-micro text-muted">{t('logsColTokens')}</dt>
              <dd className="mt-1">
                <TokenSplit
                  log={selected}
                  inLabel={t('logsTokensIn')}
                  outLabel={t('logsTokensOut')}
                  pointsLabel={(value) => t('logsTokensPoints', { value })}
                />
              </dd>
            </div>
          </dl>

          <MessageShape log={selected} />

          {selected.error_message ? (
            <div className="rounded-lg bg-surface-secondary px-3 py-2 text-xs leading-5 text-muted">
              {selected.error_kind ? <span className="mono mr-2 text-muted">{selected.error_kind}</span> : null}
              {selected.error_message}
            </div>
          ) : null}

          <StreamDiagnostics log={selected} />
          <UsageDetail log={selected} />

          <div>
            <div className="text-xs font-medium text-muted">{t('logsAttempts')}</div>
            {selected.attempts?.length ? (
              <div className="mt-2 divide-y divide-separator overflow-hidden rounded-lg bg-surface-secondary">
                {selected.attempts.map((attempt) => (
                  <div key={attempt.id} className="grid gap-1 px-3 py-2.5 text-xs sm:grid-cols-[48px_minmax(0,1fr)_auto]">
                    <div className="mono text-muted">#{attempt.attempt_index}</div>
                    <div>
                      <div className="font-medium">{attempt.account_id || '—'}</div>
                      <div className="mt-0.5 text-muted">{attempt.error_message || attempt.status}</div>
                    </div>
                    <Chip size="sm" variant="soft" color={statusColor(attempt.status === 'failover' ? 'canceled' : attempt.status)}>
                      {attempt.status}
                    </Chip>
                  </div>
                ))}
              </div>
            ) : (
              <p className="mt-2 text-xs text-muted">{t('logsNoAttempts')}</p>
            )}
          </div>
        </div>
      ) : null}
    </Sheet>
  )
}

function MessageShape({ log }: { log: RequestLog }) {
  const { t } = useI18n()
  if (!log.message_count) return null
  return (
    <div className="rounded-lg bg-surface-secondary px-3 py-3 text-xs">
      <div className="font-medium text-muted">{t('logsMessageShape')}</div>
      <dl className="mt-2 grid gap-2 sm:grid-cols-2">
        <div>
          <dt className="text-micro text-muted">{t('logsMessageCount')}</dt>
          <dd className="mono mt-0.5">{log.message_count}</dd>
        </div>
        <div>
          <dt className="text-micro text-muted">{t('logsEmptyMessageIndexes')}</dt>
          <dd className="mono mt-0.5 break-all">{log.empty_message_indexes?.length ? log.empty_message_indexes.join(', ') : '—'}</dd>
        </div>
        <div className="sm:col-span-2">
          <dt className="text-micro text-muted">{t('logsMessageRoles')}</dt>
          <dd className="mono mt-0.5 break-all">{log.message_roles?.join(' → ') || '—'}</dd>
        </div>
      </dl>
    </div>
  )
}

function StreamDiagnostics({ log }: { log: RequestLog }) {
  const { t } = useI18n()
  const diagnostic = log.stream_diagnostic
  if (!diagnostic) return null
  return (
    <div className="rounded-lg bg-surface-secondary px-3 py-3 text-xs">
      <div className="font-medium text-muted">{t('logsStreamDiagnostics')}</div>
      <dl className="mt-2 grid gap-2 sm:grid-cols-2">
        {[
          [t('logsCancellationSource'), diagnostic.cancellation_source || '—'],
          [t('logsUpstreamStatus'), diagnostic.upstream_status ?? '—'],
          [t('logsSSEEvents'), diagnostic.sse_event_count],
          [t('logsBytesRead'), diagnostic.bytes_read],
          [t('logsLastEvent'), diagnostic.last_event || '—'],
          [t('logsStreamComplete'), diagnostic.saw_done ? 'yes' : 'no'],
        ].map(([label, value]) => (
          <div key={String(label)}>
            <dt className="text-micro text-muted">{label}</dt>
            <dd className="mono mt-0.5 break-all">{value}</dd>
          </div>
        ))}
      </dl>
      {diagnostic.context_err || diagnostic.relay_error ? (
        <div className="mt-2 space-y-1 break-all text-muted">
          {diagnostic.context_err ? <div>{t('logsStreamContextErr')}: {diagnostic.context_err}</div> : null}
          {diagnostic.relay_error ? <div>{t('logsStreamRelayErr')}: {diagnostic.relay_error}</div> : null}
        </div>
      ) : null}
    </div>
  )
}

function UsageDetail({ log }: { log: RequestLog }) {
  const { t } = useI18n()
  const usage = log.usage_detail
  if (!usage) return null
  return (
    <div className="rounded-lg bg-surface-secondary px-3 py-3 text-xs">
      <div className="font-medium text-muted">{t('logsUsageDetail')}</div>
      <dl className="mt-2 grid gap-2 sm:grid-cols-2">
        {[
          [t('logsCreditConsumed'), `${formatCredit(usage.credit)} ${usage.unit || 'credits'}`],
          [t('logsColProvider'), usage.provider || '—'],
        ].map(([label, value]) => (
          <div key={String(label)}>
            <dt className="text-micro text-muted">{label}</dt>
            <dd className="mono mt-0.5 break-all">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}
