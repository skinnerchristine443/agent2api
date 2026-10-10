import { Button } from '@heroui/react'
import { Plus, X } from '@phosphor-icons/react'

import { FilterSelect } from '@/components/ui/FilterSelect'
import { SearchBar } from '@/components/ui/SearchBar'
import { Segmented } from '@/components/ui/Segmented'
import { StatusDot, type StatusDotState } from '@/components/ui/StatusDot'
import type { Translate } from '@/i18n/messages'

import type { AccountQuickFilter, AccountSortMode, AccountStateFilter } from './useAccountFilters'

/** 渠道页签（批次 8 起：workbuddy / trae；id 即渠道键，落 `?provider=`）。 */
export type ChannelTab = { id: string; label: string; count: number }

/** 快捷筛选 id → StatusDot 语义色（收敛原 `.qf-dot` 的三种 kind）。 */
function quickDotState(id: string): StatusDotState {
  if (id === 'avail') return 'ok'
  if (id === 'attn') return 'danger'
  return 'warn'
}

type Counts = { total: number; avail: number; attn: number; transit: number }

type Props = {
  counts: Counts
  quick: AccountQuickFilter
  onQuick: (value: AccountQuickFilter) => void
  /** 有任何筛选（搜索 / 状态 / 快捷）生效时显示「清除筛选」。 */
  filterActive: boolean
  onClear: () => void
  query: string
  onQueryChange: (value: string) => void
  state: AccountStateFilter
  onStateChange: (value: AccountStateFilter) => void
  sort: AccountSortMode
  onSortChange: (value: AccountSortMode) => void
  /** 渠道页签（按固定序派生自带账号渠道；点选即切换 `?provider=`）。 */
  channels: ChannelTab[]
  channel: string
  onChannelChange: (value: string) => void
  refreshing: boolean
  onRefreshCredits: () => void
  batchPending: boolean
  /** 批量签到结果文本（'' = 尚未执行）。 */
  batchHint: string
  /** 当前渠道无可签到账号时禁用批量签到。 */
  batchDisabled: boolean
  onBatchCheckin: () => void
  onAdd: () => void
  t: Translate
}

/**
 * 账号池工具栏（批次 13 布局：行 1 = 渠道页签（左）｜ 统计四格（右）；
 * 行 2 = 搜索 + 状态/排序下拉 + 清除筛选（左）｜ **页签联动动作**
 * （批量签到 / 刷新积分 / 添加账号——均作用于当前页签渠道）右对齐）。
 */
export function AccountsToolbar({
  counts,
  quick,
  onQuick,
  filterActive,
  onClear,
  query,
  onQueryChange,
  state,
  onStateChange,
  sort,
  onSortChange,
  channels,
  channel,
  onChannelChange,
  refreshing,
  onRefreshCredits,
  batchPending,
  batchHint,
  batchDisabled,
  onBatchCheckin,
  onAdd,
  t,
}: Props) {
  const cells: Array<{ id: AccountQuickFilter; label: string; count: number }> = [
    { id: 'all', label: t('accountCount'), count: counts.total },
    { id: 'avail', label: t('availableAccounts'), count: counts.avail },
    { id: 'attn', label: t('needsAttention'), count: counts.attn },
    { id: 'transit', label: t('coolingTransit'), count: counts.transit },
  ]

  return (
    <div className="space-y-2.5" data-gsap-reveal>
      {/* 行 1：渠道页签（左）｜ 统计四格（右） */}
      <div className="flex flex-wrap items-center gap-2.5">
        <Segmented
          ariaLabel={t('navAccounts')}
          value={channel}
          onChange={onChannelChange}
          items={channels.map((item) => ({ id: item.id, label: item.label, count: item.count }))}
        />
        <span className="flex-1" />
        <div className="qf-bar" role="group" aria-label={t('quickFilters')}>
          {cells.map((cell) => {
            const active = quick === cell.id
            return (
              <button
                key={cell.id}
                type="button"
                className={`qf-cell${active ? ' is-active' : ''}`}
                data-kind={cell.id}
                aria-pressed={active}
                onClick={() => onQuick(active ? 'all' : cell.id)}
              >
                <span className="n mono">{cell.count}</span>
                <span className="l">
                  {cell.id !== 'all' ? <StatusDot state={quickDotState(cell.id)} /> : null}
                  {cell.label}
                </span>
              </button>
            )
          })}
        </div>
      </div>

      {/* 行 2：搜索 + 筛选 + 清除（左）｜ 联动动作（右） */}
      <div className="flex flex-wrap items-center gap-2.5">
        <SearchBar
          className="w-full sm:w-48"
          value={query}
          onChange={onQueryChange}
          placeholder={t('searchAccounts')}
          ariaLabel={t('searchAccounts')}
        />
        <FilterSelect
          value={state === 'all' ? '' : state}
          onChange={(next) => onStateChange((next || 'all') as AccountStateFilter)}
          ariaLabel={t('filterAll')}
          options={[
            { id: '', label: t('filterAll') },
            { id: 'available', label: t('availableAccounts') },
            { id: 'attention', label: t('needsAttention') },
            { id: 'disabled', label: t('disabled') },
          ]}
        />
        <FilterSelect
          value={sort}
          onChange={(next) => onSortChange((next || 'priority') as AccountSortMode)}
          ariaLabel={t('sortBy')}
          options={[
            { id: 'priority', label: t('sortPriority') },
            { id: 'quota', label: t('sortQuotaAsc') },
            { id: 'fresh', label: t('sortFresh') },
            { id: 'severity', label: t('sortSeverity') },
          ]}
        />
        {filterActive ? (
          <button type="button" className="qf-clear" onClick={onClear}>
            <X size={13} />{t('clearFilters')}
          </button>
        ) : null}
        <span className="flex-1" />
        <Button
          size="sm"
          variant="secondary"
          isPending={batchPending}
          isDisabled={batchDisabled}
          onPress={onBatchCheckin}
        >
          {t('batchCheckin')}
        </Button>
        <Button size="sm" variant="secondary" isPending={refreshing} onPress={onRefreshCredits}>{t('refreshCredits')}</Button>
        <Button size="sm" onPress={onAdd}><Plus size={14} />{t('addAccount')}</Button>
        {batchHint ? <span className="text-xs text-muted">{batchHint}</span> : null}
      </div>
    </div>
  )
}
