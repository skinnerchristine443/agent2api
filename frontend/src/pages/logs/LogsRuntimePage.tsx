import { useEffect, useMemo, useState } from 'react'
import { Button } from '@heroui/react'
import { ArrowClockwise } from '@phosphor-icons/react'

import type { RuntimeLogEntry } from '@/api/logs'
import { ObserveTabs } from '@/components/observe/ObserveTabs'
import { FilterSearchSelect } from '@/components/ui/FilterSearchSelect'
import { FilterToggle } from '@/components/ui/FilterToggle'
import { ListPager } from '@/components/ui/ListPager'
import type { PageSize } from '@/components/ui/pagination'
import { PageAlert } from '@/components/ui/PageAlert'
import { PageHeader } from '@/components/ui/PageHeader'
import { StatusDot } from '@/components/ui/StatusDot'
import { SearchBar } from '@/components/ui/SearchBar'
import { useSheetUrlParam } from '@/components/ui/useSheetUrlParam'
import { useI18n } from '@/hooks/I18nContext'
import { useLogsFilterOptions, useRuntimeLogsQuery } from '@/hooks/useLogsQueries'
import type { RuntimeFilter } from '@/lib/logsFormat'

import { LogsRuntimeList } from './LogsRuntimeList'
import { RuntimeDetailSheet } from './RuntimeDetailSheet'
import { useRuntimeLogsFilters } from './useLogsFilters'

/**
 * 运行日志页（`/logs/runtime`）：级别 / 账号 / 搜索筛选（URL 化，搜索 280ms
 * 防抖）、详情 `?entry=<n>` 寻址、**仅第 1 页 3s 自动刷新**（翻页 / 切走即停，
 * 非交互「实时」指示只在第 1 页出现）。
 */
export function LogsRuntimePage() {
  const { t } = useI18n()
  const { filters, patch, clear, hasFilters, search } = useRuntimeLogsFilters()
  const { query, items, total, page, size, pageCount, setPage, setSize } = useRuntimeLogsQuery(filters)
  const { accountOptions, accountNameById } = useLogsFilterOptions()
  const sheet = useSheetUrlParam('entry')

  const shownLabel = useMemo(() => t('logsShownTotal', {
    shown: items.length ? `${(page - 1) * size + 1}–${Math.min(total, page * size)}` : 0,
    total,
  }), [items.length, page, size, total, t])

  // 运行日志无单条接口：详情数据按 URL 的 id 从当前页列表里取。轮询每 3s 换一
  // 次列表，命中的那条可能被新日志挤出当前页 ⇒ 命中时钉住快照（按 id 记），
  // 避免详情在阅读中途变成「找不到」（日志条目只写不改，快照不会过期）。
  const liveEntry = useMemo(
    () => (sheet.value == null ? null : items.find((item) => String(item.id) === sheet.value) ?? null),
    [items, sheet.value],
  )
  const [pinned, setPinned] = useState<{ id: string; entry: RuntimeLogEntry } | null>(null)
  useEffect(() => {
    if (sheet.value && liveEntry) setPinned({ id: sheet.value, entry: liveEntry }) // eslint-disable-line react/set-state-in-effect -- 命中详情条目即钉住快照
  }, [liveEntry, sheet.value])
  const selectedEntry = liveEntry ?? (pinned && pinned.id === sheet.value ? pinned.entry : null)

  return (
    <div className="space-y-6">
      <div data-gsap-reveal>
        <ObserveTabs />
      </div>
      <PageHeader
        description={t('pageDescLogsRuntime')}
        actions={(
          <Button
            size="sm"
            variant="secondary"
            isPending={query.loading || query.refreshing}
            onPress={() => void query.refresh()}
          >
            <ArrowClockwise size={15} />{t('refresh')}
          </Button>
        )}
      />

      {query.error ? <PageAlert title={t('failedLogs', { msg: query.error })} /> : null}

      <div className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <SearchBar
              className="sm:w-72"
              value={search.value}
              onChange={search.setValue}
              placeholder={t('logsSearchRuntime')}
              ariaLabel={t('logsSearchRuntime')}
            />
            <FilterSearchSelect
              ariaLabel={t('logsColAccount')}
              value={filters.account}
              onChange={(next) => patch({ account: next })}
              options={accountOptions}
              allLabel={t('logsFilterAccountAll')}
              searchPlaceholder={t('logsSearchAccount')}
              emptyLabel={t('logsNoFilterOptions')}
            />
            <FilterToggle
              value={filters.level}
              onChange={(next) => patch({ level: next as RuntimeFilter })}
              ariaLabel={t('logsLevelAll')}
              options={[
                { id: 'all', label: t('logsLevelAll') },
                { id: 'info', label: t('logsLevelInfo') },
                { id: 'warn', label: t('logsLevelWarn') },
                { id: 'error', label: t('logsLevelError') },
              ]}
            />
            {hasFilters ? (
              <Button size="sm" variant="ghost" onPress={clear}>{t('clearFilters')}</Button>
            ) : null}
          </div>
          <div className="flex items-center gap-2 text-xs text-muted">
            {page === 1 ? (
              <>
                <StatusDot state="ok" />
                {t('logsAutoRefresh')}
              </>
            ) : null}
            <span className="mono">{shownLabel}</span>
          </div>
        </div>

        <LogsRuntimeList
          items={items}
          loading={query.loading}
          hasFilters={hasFilters}
          onOpenEntry={(id) => sheet.open(String(id))}
          onClearFilters={clear}
          accountNameById={accountNameById}
        />

        <ListPager
          total={total}
          page={page}
          pageCount={pageCount}
          pageSize={size as PageSize}
          loading={query.loading}
          pageSizeLabel={t('logsPageSize')}
          pageLabel={t('logsPage', { page, pages: pageCount })}
          prevLabel={t('logsPrevPage')}
          nextLabel={t('logsNextPage')}
          onPage={setPage}
          onPageSize={setSize}
        />
      </div>

      <RuntimeDetailSheet
        entry={selectedEntry}
        isOpen={sheet.isOpen}
        onOpenChange={sheet.onOpenChange}
        missing={sheet.isOpen && !selectedEntry && !query.loading}
        accountNameById={accountNameById}
      />
    </div>
  )
}
