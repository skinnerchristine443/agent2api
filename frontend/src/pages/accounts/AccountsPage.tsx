import { useCallback, useMemo, useReducer, useState } from 'react'
import { Button } from '@heroui/react'
import { MagnifyingGlass, Plus } from '@phosphor-icons/react'

import { useAccountPool } from '@/components/accounts/useAccountPool'
import {
  useDeviceLoginPoll,
  type DeviceLoginOutcome,
  type DeviceLoginStatus,
} from '@/components/accounts/useDeviceLoginPoll'
import { BrandMark } from '@/components/brand/BrandMark'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { PageAlert } from '@/components/ui/PageAlert'
import { AccountsPageSkeleton } from '@/components/ui/skeletons'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import { useI18n } from '@/hooks/I18nContext'
import { accountState, quickCategory, severityRank } from '@/lib/account'
import { accountChannelKey, accountChannelLabelByKey } from '@/lib/provider'

import { AccountsActionView } from './AccountsActionView'
import { AccountsList } from './AccountsList'
import { AccountsModals } from './AccountsModals'
import { AccountsToolbar } from './AccountsToolbar'
import { checkinPolicyFor } from './accountPolicy'
import { createAccountHandlers, createAccountRunner } from './accountActions'
import { accountsReducer, initialAccountsUiState } from './accountsReducer'
import type { RowWiring } from './RowWiring'
import { useAccountFilters } from './useAccountFilters'

/** 卡片内联登录的轮询上限（迁移前：60 次 × 2s）。 */
const PAGE_DEVICE_LOGIN_ATTEMPTS = 60

/** 渠道键固定序（批次 15：渠道 × 区域组合）：其余按名称排在最后。 */
const CHANNEL_ORDER = ['workbuddy-cn', 'workbuddy-global', 'trae-cn']

/** 账号渠道键（批次 15：`<provider>-<region>`；region 缺省退化为 provider）。 */
function channelOf(account: { provider?: string; region?: string }) {
  return accountChannelKey(account.provider, account.region)
}

export function AccountsPage() {
  const { t } = useI18n()
  const pool = useAccountPool()
  const [state, dispatch] = useReducer(accountsReducer, initialAccountsUiState)
  const filters = useAccountFilters()
  const action = useAsyncAction(createAccountRunner(dispatch))
  const run = action.run
  const [batchHint, setBatchHint] = useState('')
  const [addPreset, setAddPreset] = useState('')

  const onDeviceLoginStatus = useCallback((login: DeviceLoginStatus, id: string) => {
    dispatch({ type: 'transient', id, patch: { note: login.message || t('waitingLogin') } })
  }, [t])

  const onDeviceLoginFinish = useCallback((outcome: DeviceLoginOutcome, id: string, message: string) => {
    if (outcome === 'cancelled') {
      dispatch({ type: 'transient', id, patch: { note: t('wizardLoginCancelled') } })
      return
    }
    void run(id, 'device', async () => {
      if (outcome === 'timeout') dispatch({ type: 'transient', id, patch: { note: t('wizardLoginTimeout') } })
      if (outcome === 'error') dispatch({ type: 'transient', id, patch: { note: message || t('authFailed') } })
      const { failed } = await pool.refreshQuota([id])
      if (failed.length) throw new Error(failed[0].message)
    })
  }, [pool, run, t])

  const deviceLogin = useDeviceLoginPoll({
    attempts: PAGE_DEVICE_LOGIN_ATTEMPTS,
    onStatus: onDeviceLoginStatus,
    onFinish: onDeviceLoginFinish,
  })

  const rows = pool.accounts
  const hasAccounts = rows.length > 0
  const displayRows = useMemo(() => rows.map((account) => {
    const override = state.overrides[account.id]
    return override ? { ...account, ...override } : account
  }), [rows, state.overrides])

  const quickCounts = useMemo(() => {
    const counts = { total: displayRows.length, avail: 0, attn: 0, transit: 0 }
    for (const account of displayRows) {
      const category = quickCategory(account)
      if (category === 'avail') counts.avail += 1
      else if (category === 'attn') counts.attn += 1
      else if (category === 'transit') counts.transit += 1
    }
    return counts
  }, [displayRows])
  const channelTabs = useMemo(() => {
    const counts = new Map<string, number>()
    for (const account of displayRows) {
      const key = channelOf(account)
      counts.set(key, (counts.get(key) || 0) + 1)
    }
    const rank = (key: string) => {
      const index = CHANNEL_ORDER.indexOf(key)
      return index === -1 ? CHANNEL_ORDER.length : index
    }
    return [...counts.entries()]
      .sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
      .map(([id, count]) => ({
        id,
        count,
        // 展示名走渠道键映射（WorkBuddy CN / Trae CN …）。
        label: accountChannelLabelByKey(id),
      }))
  }, [displayRows])

  // 当前页签：URL `?provider=` 命中页签就用它，否则落到固定序第一个（默认 workbuddy）。
  const channel = channelTabs.some((tab) => tab.id === filters.provider)
    ? filters.provider
    : channelTabs[0]?.id ?? ''

  // 当前渠道全量账号（页签联动动作的作用域：刷新积分 / 批量签到）。
  const channelRows = useMemo(
    () => displayRows.filter((account) => channelOf(account) === channel),
    [channel, displayRows],
  )

  const filteredRows = useMemo(() => {
    const normalized = filters.query.trim().toLowerCase()
    return displayRows.filter((account) => {
      const current = accountState(account)
      const matchesState = filters.state === 'all'
        || (filters.state === 'available' && (current === 'hot' || current === 'ready'))
        || (filters.state === 'attention' && account.enabled && current !== 'hot' && current !== 'ready')
        || (filters.state === 'disabled' && current === 'disabled')
      if (!matchesState) return false
      if (filters.quick !== 'all' && quickCategory(account) !== filters.quick) return false
      if (channel && channelOf(account) !== channel) return false
      if (!normalized) return true
      return [account.name, account.id, account.remote_uid, account.provider, account.auth_type]
        .some((value) => String(value || '').toLowerCase().includes(normalized))
    })
  }, [channel, displayRows, filters.query, filters.quick, filters.state])

  // 渠道页签是导航性切换，不计入「筛选生效」（不触发跳过渐进加载）。
  const filterActive = Boolean(filters.query.trim() || filters.state !== 'all' || filters.quick !== 'all')
  const actionRows = useMemo(() => [...filteredRows].sort((left, right) => (
    severityRank(left) - severityRank(right) || String(left.name || left.id).localeCompare(String(right.name || right.id))
  )), [filteredRows])

  const handlers = createAccountHandlers({
    dispatch,
    authPanelId: state.panels.auth,
    transients: state.transients,
    pool,
    run,
    deviceLogin,
    t,
  })

  const wiring: RowWiring = {
    busyKindFor: (id) => {
      if (state.busy?.id === id) return state.busy.kind
      return deviceLogin.accountId === id ? 'device' : ''
    },
    transients: state.transients,
    providers: pool.providers,
    handlers,
    t,
    focusId: filters.focus,
  }

  // 页签联动动作（批次 9）：作用域 = 当前渠道全量账号。
  const refreshCredits = useAsyncAction(async () => {
    const { failed } = await pool.refreshQuota(channelRows.map((account) => account.id))
    for (const item of failed) dispatch({ type: 'transient', id: item.id, patch: { note: item.message } })
  })

  const checkinIds = useMemo(
    () => channelRows
      .filter((account) => account.enabled && checkinPolicyFor(pool.providers, account))
      .map((account) => account.id),
    [channelRows, pool.providers],
  )
  const batchCheckin = useAsyncAction(async () => {
    const { ok, failed } = await pool.checkinMany(checkinIds)
    setBatchHint(t('batchCheckinDone', { ok: ok.length, failed: failed.length }))
    for (const item of failed) dispatch({ type: 'transient', id: item.id, patch: { note: item.message } })
  })

  const openAdd = useCallback(() => {
    setAddPreset(channel)
    dispatch({ type: 'addOpen', open: true })
  }, [channel])

  if (pool.loading && !hasAccounts) return <AccountsPageSkeleton />

  return (
    <div className="space-y-5">
      {pool.error ? <PageAlert title={pool.error} /> : null}

      {hasAccounts ? (
        <AccountsToolbar
          counts={quickCounts}
          quick={filters.quick}
          onQuick={filters.setQuick}
          filterActive={filterActive}
          onClear={filters.clear}
          query={filters.draftQuery}
          onQueryChange={filters.setQuery}
          state={filters.state}
          onStateChange={filters.setState}
          sort={filters.sort}
          onSortChange={filters.setSort}
          channels={channelTabs}
          channel={channel}
          onChannelChange={filters.setProvider}
          refreshing={refreshCredits.pending}
          onRefreshCredits={() => void refreshCredits.run()}
          batchPending={batchCheckin.pending}
          batchHint={batchHint}
          batchDisabled={checkinIds.length === 0}
          onBatchCheckin={() => void batchCheckin.run()}
          onAdd={openAdd}
          t={t}
        />
      ) : null}

      <AccountsModals
        panels={state.panels}
        rows={displayRows}
        busy={state.busy}
        addOpen={state.addOpen}
        addPresetProvider={addPreset}
        providers={pool.providers}
        t={t}
        onAddOpenChange={(open) => dispatch({ type: 'addOpen', open })}
        onAdded={() => void pool.reload()}
        onClosePanel={(panel) => dispatch({ type: 'panel', panel, id: null })}
        onConfirmDelete={(id) => {
          dispatch({ type: 'panel', panel: 'confirm', id: null })
          handlers.onDelete(id)
        }}
        transients={state.transients}
        handlers={handlers}
      />

      {!hasAccounts ? (
        <EmptyPanel
          bordered
          icon={<BrandMark size={28} />}
          title={t('noAccounts')}
          hint={t('accountEmptyHint')}
          action={<Button size="sm" onPress={openAdd}><Plus size={14} />{t('addAccount')}</Button>}
        />
      ) : null}

      {hasAccounts && !filteredRows.length ? (
        <EmptyPanel
          bordered
          icon={<MagnifyingGlass size={22} />}
          title={t('noAccountsMatch')}
          action={<Button size="sm" variant="ghost" onPress={filters.clear}>{t('clearFilters')}</Button>}
        />
      ) : null}

      {filteredRows.length ? (
        filters.quick === 'attn' || filters.quick === 'transit' ? (
          <AccountsActionView
            kind={filters.quick}
            title={filters.quick === 'attn' ? t('needsAttention') : t('coolingTransit')}
            rows={actionRows}
            onClear={filters.clear}
            wiring={wiring}
          />
        ) : (
          <AccountsList
            rows={filteredRows}
            sort={filters.sort}
            wiring={wiring}
          />
        )
      ) : null}
    </div>
  )
}
