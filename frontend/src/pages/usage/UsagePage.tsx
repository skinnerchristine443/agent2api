import { Button, Chip } from '@heroui/react'
import { ChartLine } from '@phosphor-icons/react'

import { ObserveTabs } from '@/components/observe/ObserveTabs'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { PageAlert } from '@/components/ui/PageAlert'
import { PageHeader } from '@/components/ui/PageHeader'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { UsageTrendChart } from '@/components/usage/UsageTrendChart'
import { useAccountNameMap } from '@/hooks/useAccountNameMap'
import { useI18n } from '@/hooks/I18nContext'
import { useUsageStats } from '@/hooks/useUsageStats'

import { UsageGroupTable } from './UsageGroupTable'
import { UsageMetrics } from './UsageMetrics'
import { UsageModelAccountTable } from './UsageModelAccountTable'
import { USAGE_WINDOWS, usageWindowLabelKey } from './usageWindow'
import { useUsageWindow } from './useUsageWindow'

/**
 * 用量统计 `/usage`（观测域迁移批次）。
 *
 * - 窗口 `?window=1d|7d|30d` 全量 URL 化（默认 7d 不写 URL），映射 API `days`；
 * - 指标行 / 趋势图 / 三表（按模型 · 按账号 · 模型 × 账号）组件化；
 * - 页面标题由 AppHeader（nav 单一事实源）承担——本页不再渲染页内 h2，
 *   `usageLead` 作为页内说明行保留（承载聚合口径，不重复标题）。
 */
export function UsagePage() {
  const { t } = useI18n()
  const { window, days, setWindow } = useUsageWindow()
  const { data: stats, error, loading } = useUsageStats(days)
  // 用量聚合按「账号内部 id」分组（后端 request_logs.account_id），这里统一映射成
  // 账号名再展示——否则表格里看到的是一串 id 而不是用户设的名字。
  const accountNameById = useAccountNameMap()
  const accountName = (id: string) => accountNameById.get(id) || id
  const windowLabel = t(usageWindowLabelKey(window))

  return (
    <div className="space-y-5">
      <div data-gsap-reveal>
        <ObserveTabs />
      </div>
      <div data-gsap-reveal>
        <PageHeader
          className="border-b border-separator pb-6"
          description={t('usageLead')}
          actions={(
            <div className="flex items-center gap-1.5" role="group" aria-label={t('usageWindowAria')}>
              {USAGE_WINDOWS.map((item) => (
                <Button
                  key={item}
                  size="sm"
                  variant={item === window ? 'primary' : 'ghost'}
                  onPress={() => setWindow(item)}
                >
                  {t(usageWindowLabelKey(item))}
                </Button>
              ))}
            </div>
          )}
        />
      </div>

      {error ? <PageAlert title={t('usageFailed', { msg: error })} /> : null}

      {loading && !stats ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
          {[0, 1, 2, 3, 4].map((index) => (
            <SkeletonBlock key={index} className="h-20 w-full rounded-2xl" />
          ))}
        </div>
      ) : stats ? (
        <>
          <UsageMetrics totals={stats.totals} />

          <SectionCard
            title={t('usageTrend')}
            hint={t('usageTrendHint')}
            right={<Chip size="sm" variant="soft">{t('usageWindowAria')} · {windowLabel}</Chip>}
          >
            {stats.daily.length === 0 ? (
              <EmptyPanel title={t('usageEmptyDaily')} icon={<ChartLine size={20} />} />
            ) : (
              <UsageTrendChart
                daily={stats.daily}
                promptLabel={t('usagePrompt')}
                completionLabel={t('usageCompletion')}
                requestsLabel={t('usageRequests')}
              />
            )}
          </SectionCard>

          <div className="grid gap-5 xl:grid-cols-2">
            <UsageGroupTable
              title={t('usageByModel')}
              hint={t('usageByModelHint')}
              empty={t('usageEmptyModels')}
              groups={stats.models}
            />
            <UsageGroupTable
              title={t('usageByAccount')}
              hint={t('usageByAccountHint')}
              empty={t('usageEmptyAccounts')}
              groups={stats.accounts}
              nameOf={accountName}
            />
          </div>

          <UsageModelAccountTable
            title={t('usageByModelAccount')}
            hint={t('usageByModelAccountHint')}
            empty={t('usageEmptyModelAccounts')}
            rows={stats.model_accounts}
            nameOf={accountName}
          />
        </>
      ) : null}
    </div>
  )
}
