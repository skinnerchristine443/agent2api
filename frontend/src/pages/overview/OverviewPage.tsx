import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'

import { FilterToggle } from '@/components/ui/FilterToggle'
import { PageAlert } from '@/components/ui/PageAlert'
import { OverviewPageSkeleton } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'
import { useOverview } from '@/hooks/OverviewContext'
import { useOverviewQueries } from '@/hooks/useOverviewQueries'

import { Inbox } from './Inbox'
import { RankBoard } from './RankBoard'
import { ResourcesLine } from './ResourcesLine'
import { StatsCards } from './StatsCards'
import { WorkbenchHealth } from './WorkbenchHealth'
import { buildInboxEvents } from './inboxEvents'
import { EMPTY_STATS, STATS_WINDOW_HOURS, statsWindowLabelKey } from './overviewShared'
import { useSummaryRefresh } from './useSummaryRefresh'
import { parseWindowParam, writeWindowParam, type StatsWindow } from './windowParam'

/**
 * 工作台（总览域 · 主方案 §3.1 / 设计 D16）：一屏判健康——
 *   ① 顶部健康条（运行状态 + 账号计数合并，单一事实源；点计数跳账号池）；
 *   ② 待办收件箱（第一公民，五类事件按紧急度排序、点击跳转处置）；
 *   ③ 关键指标卡（窗口切换；趋势以 sparkline 压缩形态保留）；
 *   ④ 进程资源占用（细行）。
 *
 * 五路取数（账号+模型 / 请求统计 / 资源 / 配额告警 / 系统设置）封装在
 * `useOverviewQueries`；运行摘要由 `OverviewContext` 供侧栏与页头消费。
 */
export function OverviewPage() {
  const { t } = useI18n()
  const { overview, loading, error } = useOverview()
  const [searchParams, setSearchParams] = useSearchParams()
  const hours = parseWindowParam(searchParams)
  const { directory, stats, resources, alerts, settings, refreshAll } = useOverviewQueries(hours)

  // 全局刷新（AppHeader 按钮）后，页面数据同步刷新。
  useSummaryRefresh(overview, refreshAll)

  // `?? []` 每次渲染都新建数组 ⇒ 先经 useMemo 落成稳定引用，再进下方 useMemo 依赖
  // （否则依赖每次渲染都变，memo 恒不命中；见 useLogsQueries 的同款写法）。
  const accounts = useMemo(() => directory.data?.accounts ?? [], [directory.data])
  // 浅合并兜底：接口返回残缺对象时外层字段回落占位口径（渲染期不因缺字段崩溃）。
  const traffic = { ...EMPTY_STATS, ...(stats.data ?? {}) }
  const quotaAlerts = useMemo(() => alerts.data?.data ?? [], [alerts.data])

  const inboxEvents = useMemo(
    () => buildInboxEvents({ accounts, alerts: quotaAlerts, t }),
    [accounts, quotaAlerts, t],
  )

  if (loading && !overview) return <OverviewPageSkeleton />

  return (
    <div className="space-y-5">
      <WorkbenchHealth overview={overview} accounts={accounts} settings={settings.data} />

      {error ? <PageAlert title={t('failedOverview', { msg: error })} /> : null}
      {stats.error ? <PageAlert title={t('failedStats', { msg: stats.error })} /> : null}

      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.65fr)_minmax(0,1fr)]">
        <Inbox events={inboxEvents} loading={directory.loading || alerts.loading} />
        <RankBoard
          providers={traffic.providers || []}
          models={traffic.models}
          errors={traffic.errors}
          loading={stats.loading}
          hours={hours}
        />
      </section>

      <section className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-xs text-muted">{t('statsWindowHint', { window: t(statsWindowLabelKey(hours)) })}</span>
          <FilterToggle
            value={String(hours)}
            onChange={(next) => {
              const value = Number(next) as StatsWindow
              if (hours === value) return
              setSearchParams((prev) => writeWindowParam(prev, value), { replace: true })
            }}
            ariaLabel={t('statsTraffic')}
            options={STATS_WINDOW_HOURS.map((item) => ({ id: String(item), label: t(statsWindowLabelKey(item)) }))}
          />
        </div>
        <StatsCards stats={traffic} hours={hours} loading={stats.loading} />
      </section>

      <ResourcesLine resources={resources.data} />
    </div>
  )
}
