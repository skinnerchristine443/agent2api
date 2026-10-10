import type { DictKey, Translate } from '@/i18n/messages'
import type { RequestStats } from '@/api/logs'
import { ERROR_KIND_LABEL_KEYS } from '@/lib/logsFormat'
import { accountChannelLabelByKey } from '@/lib/provider'

import type { StatsWindow } from './windowParam'

/** 趋势图窗口选项（id 与 URL `window` 的 hours 一一对应）。 */
export const STATS_WINDOW_HOURS: StatsWindow[] = [1, 24, 168]

export function statsWindowLabelKey(hours: StatsWindow): DictKey {
  if (hours === 1) return 'statsWindow1h'
  if (hours === 168) return 'statsWindow7d'
  return 'statsWindow24h'
}

/** 统计接口未返回前的占位（与改造前 EMPTY_STATS 同口径）。 */
export const EMPTY_STATS: RequestStats = {
  window: { from: '', to: '', hours: 24 },
  totals: { requests: 0, ok: 0, incomplete: 0, error: 0, canceled: 0, streaming: 0, success_rate: 0 },
  latency: {},
  tokens: { prompt: 0, completion: 0, cache_read: 0, total: 0 },
  status: [],
  errors: [],
  models: [],
  accounts: [],
  providers: [],
  series: [],
}

/** 渠道桶展示名（批次 15：键为「渠道 × 区域」组合，如 `workbuddy-global` → WorkBuddy Global）。 */
export function familyLabel(channel: string | undefined, t: Translate) {
  if (!channel || channel === '(unknown)') return t('statsUnknown')
  return accountChannelLabelByKey(channel)
}

export function errorLabel(kind: string, t: Translate) {
  const key = ERROR_KIND_LABEL_KEYS[kind as keyof typeof ERROR_KIND_LABEL_KEYS]
  return key ? t(key) : kind
}
