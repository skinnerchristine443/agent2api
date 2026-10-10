import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from '@heroui/react'
import { ArrowClockwise } from '@phosphor-icons/react'

import { PageAlert } from '@/components/ui/PageAlert'
import { PageHeader } from '@/components/ui/PageHeader'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useExpiryQueries } from '@/hooks/useExpiryQueries'
import { useI18n } from '@/hooks/I18nContext'
import { useNowTick } from '@/hooks/useNowTick'

import { ExpirySummaryBar } from './ExpirySummaryBar'
import { ExpiryTable } from './ExpiryTable'
import { ExpiryWindowCard } from './ExpiryWindowCard'
import {
  expiryRowsFor,
  groupExpiryRows,
  type ExpiryGroupKey,
} from './expiryModel'
import { useExpiryParams } from './useExpiryParams'

/**
 * 额度到期页（账号域）：到期窗口设置（自系统页迁出）+ 到期概览条 +
 * 到期明细表（四组 / 行展开包明细 / 行刷新）。
 *
 * 数据零后端改动：`GET /api/accounts` 的 quota 到期字段在前端分组
 * （expiryModel，纯函数带单测）；窗口读写走 `/api/system/settings`（hooks 层）。
 * `?account=<id>` 支持从概览告警条目直达（高亮并展开其所在分组）。
 */
export function ExpiryPage() {
  const { t } = useI18n()
  const queries = useExpiryQueries()
  const { accountId: highlightId, clearAccount } = useExpiryParams()
  const now = useNowTick()
  const [collapsed, setCollapsed] = useState<Partial<Record<ExpiryGroupKey, boolean>>>({})
  const appliedHighlight = useRef('')

  const primarySeconds = queries.settings?.expiry_window_seconds ?? 0
  const secondarySeconds = queries.settings?.secondary_expiry_window_seconds ?? 0
  const rows = useMemo(
    () => expiryRowsFor(queries.accounts, { primarySeconds, secondarySeconds, nowMs: now }),
    [queries.accounts, primarySeconds, secondarySeconds, now],
  )
  const groups = useMemo(() => groupExpiryRows(rows), [rows])
  const highlightRow = highlightId ? rows.find((row) => row.id === highlightId) : undefined

  // 深链定位只应用一次：展开目标分组，但用户随后手动折叠不再被拉回。
  useEffect(() => {
    if (!highlightId || appliedHighlight.current === highlightId) return
    const row = rows.find((item) => item.id === highlightId)
    if (!row) return
    appliedHighlight.current = highlightId
    setCollapsed((prev) => (prev[row.group] ? { ...prev, [row.group]: false } : prev))
  }, [highlightId, rows])

  function toggleGroup(group: ExpiryGroupKey) {
    setCollapsed((prev) => ({ ...prev, [group]: !prev[group] }))
  }

  if (queries.loading && !queries.accounts.length) {
    return (
      <div className="space-y-4">
        <SkeletonBlock className="h-24 w-full rounded-2xl" />
        <SkeletonBlock className="h-20 w-full rounded-2xl" />
        <SkeletonBlock className="h-64 w-full rounded-2xl" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div data-gsap-reveal>
        <PageHeader
          className="border-b border-separator pb-6"
          description={t('pageDescExpiry')}
          actions={(
            <Button size="sm" variant="secondary" isPending={queries.reloading} onPress={queries.reload}>
              <ArrowClockwise size={15} />{t('refresh')}
            </Button>
          )}
        />
      </div>

      {queries.error ? <PageAlert title={t('expiryFailed', { msg: queries.error })} /> : null}

      {/* 顶部两栏（批次 7 起；13 等高；14 恢复 1:1——左卡加宽、右卡缩窄）：
          左 = 到期额度优先（主/次窗口设置），右 = 总计（按渠道竖向 3 行）。 */}
      <div className="grid items-stretch gap-5 xl:grid-cols-2">
        <ExpiryWindowCard
          settings={queries.settings}
          saving={queries.savingWindow}
          error={queries.saveWindowError}
          onSave={queries.saveWindow}
          t={t}
        />

        <ExpirySummaryBar rows={rows} sortDisabled={primarySeconds <= 0} t={t} />
      </div>

      {highlightRow ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-2xl border border-warning/30 bg-warning/5 px-4 py-2.5">
          <span className="text-xs text-warning">{t('expiryLocated', { name: highlightRow.name })}</span>
          <Button size="sm" variant="ghost" onPress={clearAccount}>{t('expiryClearLocation')}</Button>
        </div>
      ) : null}

      {queries.refreshError ? <PageAlert title={t('expiryRefreshFailed', { msg: queries.refreshError })} /> : null}

      <ExpiryTable
        groups={groups}
        collapsed={collapsed}
        onToggleGroup={toggleGroup}
        highlightId={highlightRow ? highlightRow.id : ''}
        refreshingId={queries.refreshingId}
        onRefresh={queries.refreshAccountQuota}
        now={now}
        t={t}
      />
    </div>
  )
}
