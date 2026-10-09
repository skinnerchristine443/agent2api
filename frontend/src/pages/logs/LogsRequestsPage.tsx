import { useMemo, useState } from 'react'
import { Button } from '@heroui/react'
import { ArrowClockwise, TrashSimple } from '@phosphor-icons/react'

import { ObserveTabs } from '@/components/observe/ObserveTabs'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { ListPager, type PageSize } from '@/components/ui/ListPager'
import { PageAlert } from '@/components/ui/PageAlert'
import { PageHeader } from '@/components/ui/PageHeader'
import { useSheetUrlParam } from '@/components/ui/useSheetUrlParam'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import { useI18n } from '@/hooks/I18nContext'
import { clearLogHistory, useLogsFilterOptions, useRequestLogsQuery } from '@/hooks/useLogsQueries'

import { LogsRequestsFilterBar } from './LogsRequestsFilterBar'
import { LogsRequestsTable } from './LogsRequestsTable'
import { RequestDetailSheet } from './RequestDetailSheet'
import { useRequestLogsFilters } from './useLogsFilters'

/**
 * 请求日志页（`/logs/requests`）：筛选全量 URL 化、详情 `?request=<id>` 寻址、
 * **手动刷新（无自动轮询）**。页面标题 / 副标题由 AppHeader 从 nav 推导，
 * 页内只渲染说明与操作（PageHeader）。
 */
export function LogsRequestsPage() {
  const { t } = useI18n()
  const { filters, patch, clear, hasFilters } = useRequestLogsFilters()
  const { query, items, total, page, size, pageCount, setPage, setSize } = useRequestLogsQuery(filters)
  const { accountOptions, modelOptions, accountNameById, providerLabel } = useLogsFilterOptions()
  const sheet = useSheetUrlParam('request')
  const clearAction = useAsyncAction(clearLogHistory)
  const [clearOpen, setClearOpen] = useState(false)

  const shownLabel = useMemo(() => t('logsShownTotal', {
    shown: items.length ? `${(page - 1) * size + 1}–${Math.min(total, page * size)}` : 0,
    total,
  }), [items.length, page, size, total, t])

  const error = query.error || clearAction.error

  async function onClear() {
    const result = await clearAction.run()
    if (result === undefined) return // 失败：错误落 clearAction.error，对话框保持打开
    setClearOpen(false)
    await query.refresh()
  }

  return (
    <div className="space-y-6">
      <div data-gsap-reveal>
        <ObserveTabs />
      </div>
      <PageHeader
        description={t('pageDescLogsRequests')}
        actions={(
          <>
            <Button
              size="sm"
              variant="ghost"
              onPress={() => setClearOpen(true)}
              isDisabled={clearAction.pending || total === 0}
            >
              <TrashSimple size={14} />{t('logsClear')}
            </Button>
            <Button
              size="sm"
              variant="secondary"
              isPending={query.loading || query.refreshing}
              onPress={() => void query.refresh()}
            >
              <ArrowClockwise size={15} />{t('refresh')}
            </Button>
          </>
        )}
      />

      {error ? <PageAlert title={t('failedLogs', { msg: error })} /> : null}

      <div className="space-y-4">
        <LogsRequestsFilterBar
          filters={filters}
          onChange={patch}
          onClear={clear}
          hasFilters={hasFilters}
          accountOptions={accountOptions}
          modelOptions={modelOptions}
          shownLabel={shownLabel}
        />
        <LogsRequestsTable
          items={items}
          loading={query.loading}
          hasFilters={hasFilters}
          onOpenRequest={sheet.open}
          onClearFilters={clear}
          accountNameById={accountNameById}
          providerLabel={providerLabel}
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

      <RequestDetailSheet
        requestId={sheet.value}
        isOpen={sheet.isOpen}
        onOpenChange={sheet.onOpenChange}
        accountNameById={accountNameById}
        providerLabel={providerLabel}
      />

      <ConfirmDialog
        isOpen={clearOpen}
        title={t('logsClear')}
        description={t('logsClearConfirm')}
        confirmLabel={t('logsClear')}
        cancelLabel={t('close')}
        closeLabel={t('close')}
        isPending={clearAction.pending}
        onClose={() => setClearOpen(false)}
        onConfirm={() => void onClear()}
      />
    </div>
  )
}
