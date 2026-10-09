import { ApiError, api } from './client'

/**
 * 成长中心（upskill）三端点：只读聚合、幂等领取、成长日志。
 *
 * 类型对齐 Go 侧 `internal/providers/growth.go`（provider 中立映射）与
 * `internal/accounts/types.go` 的 GrowthObservation；分区块独立容错体现在
 * 各区块自带的 `error` 字段与 `tasks_error`（一个区块失败不影响其余区块）。
 */
export type GrowthTravelStatus = {
  state?: string
  record_id?: string
  daily_limit_reached?: boolean
  available?: boolean
  error?: string
}

export type GrowthTask = {
  code: string
  title?: string
  locked?: boolean
  accept_status?: string
  reward_credit?: number
  reward_energy?: number
}

export type GrowthStreak = {
  days: number
  has_days?: boolean
  makeup_dates?: string[]
  error?: string
}

export type GrowthEnergy = {
  balance: number
  has_balance?: boolean
  error?: string
}

export type GrowthHeatmapCell = { date: string; score: number }

export type GrowthHeatmap = { cells: GrowthHeatmapCell[]; error?: string }

export type GrowthStatus = {
  travel: GrowthTravelStatus
  tasks: GrowthTask[]
  tasks_error?: string
  streak: GrowthStreak
  energy: GrowthEnergy
  heatmap: GrowthHeatmap
}

export type GrowthClaimStatus = 'success' | 'already_claimed' | 'skipped' | 'failed'

export type GrowthClaimOutcome = {
  target: string
  action: string
  status: GrowthClaimStatus
  message?: string
  credit?: number
  energy?: number
}

/** 领取结果：`outcomes` 为逐项尝试；`errors` 为无法尝试的区块级原因。 */
export type GrowthClaimResult = {
  outcomes: GrowthClaimOutcome[]
  errors?: string[]
}

export type GrowthObservation = {
  id: number
  account_id: string
  at: string
  target: string
  action: string
  status: string
  message?: string
  credit?: number
  energy?: number
}

export function fetchGrowthStatus(accountId: string, signal?: AbortSignal) {
  return api<GrowthStatus>(`/api/accounts/${encodeURIComponent(accountId)}/growth`, { signal })
}

export function claimGrowthRewards(accountId: string, signal?: AbortSignal) {
  return api<GrowthClaimResult>(`/api/accounts/${encodeURIComponent(accountId)}/growth/claim`, {
    method: 'POST',
    body: '{}',
    signal,
  })
}

export function fetchGrowthObservations(accountId: string, limit = 50, signal?: AbortSignal) {
  return api<{ data?: GrowthObservation[] }>(
    `/api/accounts/${encodeURIComponent(accountId)}/growth/observations?limit=${limit}`,
    { signal },
  )
}

/**
 * 渠道不支持成长中心：console 以 400 + code=provider_unsupported 返回
 * （`internal/console/service_errors.go:27`），调用方据此显式降级而非伪造空态。
 */
export function isProviderUnsupported(error: unknown) {
  return error instanceof ApiError && error.code === 'provider_unsupported'
}

/** 账号服务不可用（`growth_unavailable`，503）：可重试，区别于「渠道不支持」。 */
export function isGrowthUnavailable(error: unknown) {
  return error instanceof ApiError && error.code === 'growth_unavailable'
}
