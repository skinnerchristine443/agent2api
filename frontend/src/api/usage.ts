import { api } from './client'

export type UsageStatsWindow = {
  since: string
  until: string
  days: number
}

export type UsageStatsTotals = {
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  errors: number
  incomplete: number
  canceled: number
  cache_read_tokens: number
  cache_write_tokens: number
  cache_hit_rate: number
  output_tokens_per_second: number
}

export type UsageStatsDaily = {
  date: string
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  errors: number
  incomplete: number
  canceled: number
  cache_read_tokens: number
  output_tokens_per_second: number
}

export type UsageStatsGroup = {
  key: string
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  errors: number
  cache_read_tokens: number
  cache_hit_rate: number
  output_tokens_per_second: number
}

export type UsageStatsModelAccount = {
  model: string
  account: string
  requests: number
  total_tokens: number
  errors: number
  output_tokens_per_second: number
}

export type UsageStats = {
  window: UsageStatsWindow
  totals: UsageStatsTotals
  daily: UsageStatsDaily[]
  models: UsageStatsGroup[]
  accounts: UsageStatsGroup[]
  model_accounts: UsageStatsModelAccount[]
}

export function fetchUsageStats(days = 7, signal?: AbortSignal) {
  return api<UsageStats>(`/api/overview/usage?days=${encodeURIComponent(String(days))}`, { signal })
}
