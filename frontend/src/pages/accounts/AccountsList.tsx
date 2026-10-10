import { useEffect, useMemo, useState } from 'react'

import { ListPager } from '@/components/ui/ListPager'
import { ACCOUNT_PAGE_SIZES, type PageSize } from '@/components/ui/pagination'
import { quotaDisplay, severityRank, type AccountRow } from '@/lib/account'

import { paginate } from './accountsPaging'
import { WiredAccountRow, type RowWiring } from './RowWiring'
import type { AccountSortMode } from './useAccountFilters'

type Props = {
  rows: AccountRow[]
  /** 页内排序（默认稳定序「优先级 → 名称」）。 */
  sort: AccountSortMode
  wiring: RowWiring
}

function sortItems(items: AccountRow[], sort: AccountSortMode): AccountRow[] {
  const byName = (a: AccountRow, b: AccountRow) => String(a.name || a.id).localeCompare(String(b.name || b.id))
  const copy = [...items]
  if (sort === 'quota') {
    return copy.sort((a, b) => (quotaDisplay(a)?.remaining ?? 101) - (quotaDisplay(b)?.remaining ?? 101) || byName(a, b))
  }
  if (sort === 'severity') {
    return copy.sort((a, b) => severityRank(a) - severityRank(b) || byName(a, b))
  }
  if (sort === 'fresh') {
    // 「最近刷新」口径：账号行 updated_at（额度 / 状态更新时间）降序；缺失排最后。
    return copy.sort((a, b) => (Date.parse(b.updated_at || '') || 0) - (Date.parse(a.updated_at || '') || 0) || byName(a, b))
  }
  // 默认稳定序（设计 D12）：优先级 → 名称，不随状态重排。
  return copy.sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0) || byName(a, b))
}

/**
 * 账号池列表（批次 8：渠道页签化后不再分组——直接渲染当前渠道的账号行；
 * 批次 9：表头逐列拆分；批次 11：列序与命名对齐）。列表加载形态：
 * **分页**（每页默认 20，可切 50 / 100）——不再做「前 N 条 + 显示更多」
 * 渐进折叠；换渠道 / 筛选 / 排序 / 每页长即回第 1 页。
 */
export function AccountsList({ rows, sort, wiring }: Props) {
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<PageSize>(20)
  const { t } = wiring
  const sorted = useMemo(() => sortItems(rows, sort), [rows, sort])
  const paging = paginate(sorted, page, pageSize)

  // 切换渠道 / 筛选 / 排序 / 每页长后回到第 1 页（避免沿用上一列表的页码）。
  useEffect(() => {
    setPage(1) // eslint-disable-line react/set-state-in-effect -- 换筛选/排序/页长即回第 1 页
  }, [rows, sort, pageSize])

  return (
    <section className="acct-list" data-gsap-reveal>
      <div className="acct-head" aria-hidden="true">
        <span />
        <span>{t('accountCount')}</span>
        <span>{t('runtimeState')}</span>
        <span>{t('authentication')}</span>
        <span>{t('priority')}</span>
        <span className="col-sw-models">{t('modelRequests')}</span>
        <span className="col-sw-checkin">{t('acctColAutoCheckin')}</span>
        <span>{t('acctColCheckinResult')}</span>
        <span>{t('checkinNow')}</span>
        <span>{t('quota')}</span>
        <span>{t('recentError')}</span>
        <span>{t('refreshAccount')}</span>
        <span>{t('more')}</span>
        <span>{t('acctColEnabled')}</span>
      </div>

      {paging.slice.map((account) => (
        <WiredAccountRow key={account.id} account={account} wiring={wiring} />
      ))}

      {sorted.length > 0 ? (
        <div className="mt-3">
          <ListPager
            total={sorted.length}
            page={paging.page}
            pageCount={paging.pageCount}
            pageSize={pageSize}
            pageSizeLabel={t('acctPageSize')}
            pageLabel={t('acctPage', { page: paging.page, pages: paging.pageCount })}
            prevLabel={t('acctPrevPage')}
            nextLabel={t('acctNextPage')}
            onPage={setPage}
            onPageSize={setPageSize}
            pageSizes={ACCOUNT_PAGE_SIZES}
          />
        </div>
      ) : null}
    </section>
  )
}
