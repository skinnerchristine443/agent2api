import type { RequestStats } from '@/api/logs'
import { CountUp } from '@/components/overview/CountUp'
import { Sparkline } from '@/components/overview/Sparkline'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { StatusDot } from '@/components/ui/StatusDot'
import { useI18n } from '@/hooks/I18nContext'
import { formatCompact, formatLatency } from '@/lib/format'

import { statsWindowLabelKey } from './overviewShared'
import type { StatsWindow } from './windowParam'

/**
 * 指标卡 ×4（请求数 / 成功率 / 平均耗时 / Token 总数），数据来自请求统计聚合。
 *
 * 密度治理 §3.1：独立小卡 + 纵向压缩（v1 联排大卡的 min-h-32/p-5 收为
 * px-4/py-3.5）；**趋势以 sparkline 呈现**（请求数 / 成功率由 series
 * 真实派生——耗时 / Token 无逐点数据，不虚设）。
 */
export function StatsCards({ stats, hours, loading }: { stats: RequestStats; hours: StatsWindow; loading: boolean }) {
  const { t } = useI18n()
  const series = stats.series || []
  const metrics = [
    {
      label: t('metricRequests'),
      value: stats.totals.requests as number | null,
      kind: 'compact' as const,
      detail: t('statsWindowHint', { window: t(statsWindowLabelKey(hours)) }),
      ok: stats.totals.requests > 0,
      spark: series.map((point) => point.requests),
    },
    {
      label: t('metricSuccess'),
      value: stats.totals.success_rate,
      kind: 'percent' as const,
      detail: `${stats.totals.ok} ${t('logsFilterOk')} · ${stats.totals.incomplete} ${t('logsFilterIncomplete')} · ${stats.totals.error} ${t('logsFilterError')}`,
      ok: stats.totals.requests === 0 || stats.totals.success_rate >= 0.9,
      spark: series.map((point) => (point.requests ? (point.ok / point.requests) * 100 : 0)),
    },
    {
      label: t('metricLatency'),
      value: stats.latency.p95_ms ?? stats.latency.avg_ms,
      kind: 'ms' as const,
      detail: `p50 ${formatLatency(stats.latency.p50_ms)} · avg ${formatLatency(stats.latency.avg_ms)}`,
      ok: stats.latency.p95_ms == null || stats.latency.p95_ms < 8000,
      spark: null,
    },
    {
      label: t('metricTokens'),
      value: stats.tokens.total,
      kind: 'compact' as const,
      detail: `${formatCompact(stats.tokens.prompt)} / ${formatCompact(stats.tokens.completion)}`,
      ok: true,
      spark: null,
    },
  ]

  return (
    <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {metrics.map((metric) => (
        <div
          key={metric.label}
          data-gsap-reveal
          className="rounded-2xl border border-border bg-surface px-4 py-3.5"
        >
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-medium text-muted">{metric.label}</span>
            <StatusDot state={metric.ok ? 'ok' : 'danger'} />
          </div>
          <div className="mt-2.5 flex items-end justify-between gap-3">
            <div className="mono text-2xl font-semibold tracking-[-0.03em]">
              {loading ? <SkeletonBlock className="h-7 w-20" /> : metric.value == null ? '—' : <CountUp value={metric.value} kind={metric.kind} />}
            </div>
            {metric.spark && !loading ? (
              <Sparkline className="h-[34px] w-[92px] shrink-0" points={metric.spark} />
            ) : null}
          </div>
          <div className="mono mt-1.5 truncate text-micro text-muted">
            {loading ? <SkeletonBlock className="h-3 w-28" /> : metric.detail}
          </div>
        </div>
      ))}
    </section>
  )
}
