import { useEffect, useMemo, useState } from 'react'
import { Button, Chip } from '@heroui/react'
import { ArrowClockwise, MagnifyingGlass } from '@phosphor-icons/react'

import type { ModelInfo } from '@/api/types'
import { ModelDetailsModal } from '@/components/models/ModelDetailsModal'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { FilterSelect } from '@/components/ui/FilterSelect'
import { ListPager } from '@/components/ui/ListPager'
import { PAGE_SIZES, type PageSize } from '@/components/ui/pagination'
import { PageAlert } from '@/components/ui/PageAlert'
import { SearchBar } from '@/components/ui/SearchBar'
import { SectionCard } from '@/components/ui/SectionCard'
import { ProvidersPageSkeleton, ProvidersTableSkeleton } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'
import { useModelsCatalog } from '@/hooks/useModelsCatalog'
import { useOverview } from '@/hooks/OverviewContext'
import { usePagedQuery } from '@/hooks/usePagedQuery'
import { accountProviderLabel } from '@/lib/provider'

import { ModelCards } from './ModelCards'
import { ModelTable } from './ModelTable'
import { modelSettingsKey } from './modelMeta'
import { filterModels } from './modelsFilter'
import { useModelsFilters } from './useModelsFilters'

const DEFAULT_PAGE_SIZE: PageSize = 50

/** 每页长：URL 取值不在允许清单内时回退默认（ListPager 只认 20/50/100）。 */
function toPageSize(size: number): PageSize {
  return PAGE_SIZES.includes(size as PageSize) ? size as PageSize : DEFAULT_PAGE_SIZE
}

/**
 * 模型目录区块（批次 4b 收编，原 `/models` 独立页 → 接入页下置区块，锚点 `#models`）。
 *
 * - 筛选（`q` + `provider`）与分页（`page`/`size`）全量 URL 化（§7.3）；
 *   筛选变化自动回落第 1 页（usePagedQuery）；
 * - 表格 ≥lg / 卡片 <lg 双形态；详情模态保留（上下文窗口 / 推理强度 / max mode
 *   的 PATCH 在 useModelsCatalog 内就地生效）。
 */
export function AccessModels() {
  const { t } = useI18n()
  const { overview, loading: overviewLoading } = useOverview()
  const filters = useModelsFilters()
  const catalog = useModelsCatalog()
  const { page, size, setPage, setSize } = usePagedQuery({
    defaultSize: DEFAULT_PAGE_SIZE,
    filtersKey: filters.filtersKey,
  })
  const [detailModel, setDetailModel] = useState<ModelInfo | null>(null)

  const pageSize = toPageSize(size)
  const filtered = useMemo(
    () => filterModels(catalog.models, { q: filters.q, provider: filters.provider }, t),
    [catalog.models, filters.q, filters.provider, t],
  )
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  // 页码越界（直链 ?page=9 而结果只有 2 页）→ 收敛回末页，保持 URL 可寻址且自洽。
  useEffect(() => {
    if (page > pageCount) setPage(pageCount)
  }, [page, pageCount, setPage])

  const paged = useMemo(() => {
    const start = (currentPage - 1) * pageSize
    return filtered.slice(start, start + pageSize)
  }, [currentPage, filtered, pageSize])
  const shownFrom = filtered.length === 0 ? 0 : (currentPage - 1) * pageSize + 1
  const shownTo = Math.min(filtered.length, currentPage * pageSize)
  const shownLabel = filtered.length
    ? t('logsShownTotal', { shown: `${shownFrom}–${shownTo}`, total: filtered.length })
    : t('shownTotal', { shown: 0, total: catalog.models.length })

  if ((overviewLoading && !overview) || catalog.loading) return <ProvidersPageSkeleton />

  return (
    <div id="models" className="space-y-4">
      {catalog.message ? (
        <PageAlert status={catalog.messageError ? 'danger' : 'success'} title={catalog.message} />
      ) : null}

      <SectionCard
        title={<span className="flex items-center gap-2">{t('navModels')}<Chip size="sm" variant="soft">{filtered.length}</Chip></span>}
        hint={<>{catalog.models.length ? shownLabel : t('noModelsYet')}{' · '}{t('contextConfigHint')}</>}
        padded={false}
        right={(
          <>
            <SearchBar
              className="w-full min-w-0 sm:w-64"
              value={filters.q}
              onChange={filters.setQ}
              placeholder={t('filterPh')}
              ariaLabel={t('filter')}
            />
            <FilterSelect
              ariaLabel={t('providerCol')}
              value={filters.provider}
              onChange={filters.setProvider}
              options={[
                { id: '', label: t('providerFilterAll') },
                ...catalog.providerOptions.map((option) => ({
                  id: option.value,
                  label: accountProviderLabel(option.provider, option.region, t),
                })),
              ]}
            />
            <Button
              size="sm"
              variant="secondary"
              isPending={catalog.refreshing}
              onPress={() => void catalog.refresh()}
            >
              <ArrowClockwise size={14} />
              {catalog.refreshing ? t('refreshing') : t('refresh')}
            </Button>
          </>
        )}
      >
        {catalog.refreshing || overviewLoading ? (
          <ProvidersTableSkeleton />
        ) : filtered.length === 0 ? (
          <div className="p-4">
            <EmptyPanel
              icon={<MagnifyingGlass size={22} />}
              title={catalog.models.length ? t('noModelsMatch') : t('noProviders')}
              hint={catalog.models.length ? (filters.q || filters.provider || t('noModelsMatch')) : t('noModelsYet')}
            />
          </div>
        ) : (
          <>
            <ModelCards
              models={paged}
              savingKey={catalog.savingKey}
              onDetails={setDetailModel}
              onToggleMaxMode={(model, selected) => void catalog.onToggleMaxMode(model, selected)}
              onReasoningChange={(model, next) => void catalog.onReasoningChange(model, next)}
            />
            <ModelTable
              models={paged}
              savingKey={catalog.savingKey}
              onDetails={setDetailModel}
              onToggleMaxMode={(model, selected) => void catalog.onToggleMaxMode(model, selected)}
              onReasoningChange={(model, next) => void catalog.onReasoningChange(model, next)}
            />
          </>
        )}
      </SectionCard>

      <ModelDetailsModal
        model={detailModel
          ? catalog.models.find((item) => modelSettingsKey(item) === modelSettingsKey(detailModel)) || detailModel
          : null}
        t={t}
        onClose={() => setDetailModel(null)}
      />
      <ListPager
        total={filtered.length}
        page={currentPage}
        pageCount={pageCount}
        pageSize={pageSize}
        loading={catalog.refreshing}
        pageSizeLabel={t('logsPageSize')}
        pageLabel={t('logsPage', { page: currentPage, pages: pageCount })}
        prevLabel={t('logsPrevPage')}
        nextLabel={t('logsNextPage')}
        onPage={setPage}
        onPageSize={setSize}
      />
    </div>
  )
}
