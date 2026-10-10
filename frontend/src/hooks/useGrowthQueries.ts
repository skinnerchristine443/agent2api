import { useEffect, useRef, useState } from 'react'

import {
  claimGrowthRewards,
  fetchGrowthObservations,
  fetchGrowthOverview,
  fetchGrowthStatus,
  isGrowthUnavailable,
  isProviderUnsupported,
  type GrowthClaimResult,
  type GrowthObservation,
  type GrowthOverviewRow,
  type GrowthStatus,
} from '@/api/growth'
import { fetchAccounts, fetchProviders, type ProviderDescriptor } from '@/api/overview'
import { useApiQuery } from '@/hooks/useApiQuery'
import { useAsyncAction } from '@/hooks/useAsyncAction'
import type { AccountRow } from '@/lib/account'

/** 降级原因：渠道不支持（400 provider_unsupported）/ 账号服务未就绪（503）。 */
export type GrowthDegrade = '' | 'unsupported' | 'unavailable'

type TaggedError = { accountId: string; error: unknown }

function classify(tagged: TaggedError | null, accountId: string): GrowthDegrade {
  if (!tagged || tagged.accountId !== accountId) return ''
  if (isProviderUnsupported(tagged.error)) return 'unsupported'
  if (isGrowthUnavailable(tagged.error)) return 'unavailable'
  return ''
}

export type GrowthQueries = {
  accounts: AccountRow[]
  accountsLoading: boolean
  /** 供应商描述符（成长能力位在 `capabilities.growth`；页面据此过滤选择器）。 */
  providers: ProviderDescriptor[]
  /** providers 首取是否已成功落地——未就绪时应先不过滤，避免假空态。 */
  providersReady: boolean
  status: GrowthStatus | null
  statusLoading: boolean
  /** 非降级类失败的文案（降级时由 `degrade` 表达，不重复报错）。 */
  statusError: string | null
  observations: GrowthObservation[]
  observationsLoading: boolean
  observationsError: string | null
  claimResult: GrowthClaimResult | null
  claimPending: boolean
  claimError: string | null
  /** 当前账号的降级原因（取状态取数与领取中先出现的那个）。 */
  degrade: GrowthDegrade
  claim: () => void
  reloadStatus: () => void
  /** 跨账号任务领取总览（每个账号只读一次任务清单）。 */
  overview: GrowthOverviewRow[]
  overviewLoading: boolean
  overviewError: string | null
  reloadOverview: () => void
  /** 一键领取总览里所有「可领 > 0」的账号，返回逐账号结果。 */
  claimAllClaimable: () => void
  claimAllPending: boolean
  claimAllResult: { success: number; already: number } | null
}

/**
 * 成长中心数据层：账号清单（选择器）、只读聚合、成长日志、幂等领取。
 *
 * 取数走 `useApiQuery`（depsKey 前缀 `growth:`，enabled 随账号切换）；
 * 「渠道不支持」需要原始错误码，故在 fetcher 内留存 tagged error（含所属账号，
 * 防止切换账号后误报降级）——`useApiQuery` 对外只暴露文案，不能承载 code。
 * 领取走 `useAsyncAction`（pending 去重），结果含 `outcomes` 与 `errors[]`。
 */
export function useGrowthQueries(accountId: string): GrowthQueries {
  const accountsQuery = useApiQuery((signal) => fetchAccounts(false, signal), 'growth:accounts')
  // 静态能力位（`capabilities.growth`）：选择器据此把无成长中心的渠道整个排除。
  const providersQuery = useApiQuery((signal) => fetchProviders(signal), 'growth:providers')
  const statusErrorRef = useRef<TaggedError | null>(null)
  const claimErrorRef = useRef<TaggedError | null>(null)
  const [claimResult, setClaimResult] = useState<GrowthClaimResult | null>(null)

  // 切换账号即清空上一次的领取结果：否则旧账号的 outcomes/errors 会串台
  // 到新账号的领取区（批次 7 反馈）。
  useEffect(() => {
    setClaimResult(null)
  }, [accountId])

  const statusQuery = useApiQuery(
    async (signal) => {
      try {
        return await fetchGrowthStatus(accountId, signal)
      } catch (error) {
        statusErrorRef.current = { accountId, error }
        throw error
      }
    },
    `growth:status:${accountId}`,
    { enabled: Boolean(accountId) },
  )

  const observationsQuery = useApiQuery(
    (signal) => fetchGrowthObservations(accountId, 50, signal),
    `growth:observations:${accountId}`,
    { enabled: Boolean(accountId) },
  )

  // 总览独立于当前选中账号：进入页面即可看全部账号进度。
  const overviewQuery = useApiQuery((signal) => fetchGrowthOverview(signal), 'growth:overview')
  const overviewRefresh = overviewQuery.refresh
  const statusRefreshForAll = statusQuery.refresh
  const [claimAllResult, setClaimAllResult] = useState<{ success: number; already: number } | null>(null)

  // 一键领取：只挑「可领 > 0」的账号，逐个幂等领取（顺序执行，避免把上游
  // 打成突发）。已领过的账号不计入成功数。完成后刷新总览与当前账号。
  const claimAllAction = useAsyncAction(async () => {
    const targets = (overviewQuery.data?.rows ?? []).filter((row) => !row.error && row.claimable > 0)
    let success = 0
    let already = 0
    for (const row of targets) {
      try {
        const result = await claimGrowthRewards(row.account_id)
        for (const outcome of result.outcomes ?? []) {
          if (outcome.status === 'success') success++
          else if (outcome.status === 'already_claimed') already++
        }
      } catch {
        // 单个账号失败不中断整批；总览刷新后会如实反映它仍可领。
      }
    }
    await overviewRefresh()
    if (accountId) await statusRefreshForAll()
    const summary = { success, already }
    setClaimAllResult(summary)
    return summary
  })

  const statusRefresh = statusQuery.refresh
  const observationsRefresh = observationsQuery.refresh
  const claimAction = useAsyncAction(async () => {
    setClaimResult(null)
    try {
      const result = await claimGrowthRewards(accountId)
      setClaimResult(result)
      await statusRefresh()
      await observationsRefresh()
      return result
    } catch (error) {
      claimErrorRef.current = { accountId, error }
      throw error
    }
  })

  const active = Boolean(accountId)
  const degrade = classify(statusErrorRef.current, accountId) || classify(claimErrorRef.current, accountId)

  return {
    accounts: accountsQuery.data?.data ?? [],
    accountsLoading: accountsQuery.loading,
    providers: providersQuery.data?.data ?? [],
    providersReady: providersQuery.data !== null,
    status: active ? statusQuery.data : null,
    statusLoading: active ? statusQuery.loading : false,
    statusError: active && !degrade ? statusQuery.error : null,
    observations: active ? (observationsQuery.data?.data ?? []) : [],
    observationsLoading: active ? observationsQuery.loading : false,
    observationsError: active ? observationsQuery.error : null,
    claimResult: active ? claimResult : null,
    claimPending: claimAction.pending,
    claimError: degrade ? null : claimAction.error,
    degrade,
    claim: () => void claimAction.run(),
    reloadStatus: () => void statusQuery.refresh(),
    overview: overviewQuery.data?.rows ?? [],
    overviewLoading: overviewQuery.loading,
    overviewError: overviewQuery.error,
    reloadOverview: () => void overviewQuery.refresh(),
    claimAllClaimable: () => void claimAllAction.run(),
    claimAllPending: claimAllAction.pending,
    claimAllResult,
  }
}
