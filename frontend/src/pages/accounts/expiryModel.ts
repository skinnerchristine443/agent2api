import type { AccountQuotaPackage } from '@/api/types'
import type { DictKey, Translate } from '@/i18n/messages'
import type { AccountRow } from '@/lib/account'
import { accountChannelKey } from '@/lib/provider'

export const SECONDS_PER_DAY = 24 * 60 * 60

/** API 以秒存储窗口；控制台按整天编辑。非整天值（历史遗留 36h）渲染为精确
 *  天数，使打开本页绝不会悄悄把运维人员存储的设置取整。 */
export function secondsToDaysInput(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0'
  return String(Number((seconds / SECONDS_PER_DAY).toFixed(4)))
}

export function parseWindowDays(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '') return null
  const days = Number(trimmed)
  if (!Number.isFinite(days) || days < 0) return null
  return days
}

export function daysToSeconds(days: number): number {
  return Math.round(days * SECONDS_PER_DAY)
}

/** 主窗口（天）= 0 即关闭整套到期排序：次窗口一并存 0（与后端归一化同义）。 */
export function normalizeWindowSeconds(primarySeconds: number, secondarySeconds: number) {
  if (primarySeconds <= 0) return { primarySeconds: 0, secondarySeconds: 0 }
  return { primarySeconds, secondarySeconds }
}

export type ExpiryGroupKey = 'primary' | 'secondary' | 'later' | 'unreported'

export const EXPIRY_GROUP_ORDER: ExpiryGroupKey[] = ['primary', 'secondary', 'later', 'unreported']

const GROUP_LABEL_KEYS: Record<ExpiryGroupKey, DictKey> = {
  primary: 'expiryGroupPrimary',
  secondary: 'expiryGroupSecondary',
  later: 'expiryGroupLater',
  unreported: 'expiryGroupUnreported',
}

export function expiryGroupLabelKey(group: ExpiryGroupKey): DictKey {
  return GROUP_LABEL_KEYS[group]
}

export type ExpiryRow = {
  id: string
  name: string
  provider: string
  region: string
  /** 最早的包过期时刻（Unix 秒）；0 = 该渠道未上报。 */
  expiresAt: number
  expiringRemain: number
  unit: string
  packages: AccountQuotaPackage[]
  group: ExpiryGroupKey
}

export type UnitTotal = { unit: string; amount: number }

export type ExpirySummary = {
  primary: { count: number; totals: UnitTotal[] }
  secondary: { count: number; totals: UnitTotal[] }
  later: { count: number; totals: UnitTotal[] }
  unreported: { count: number }
}

/**
 * 到期明细行：`GET /api/accounts` 的 quota 字段在前端分组（零后端改动）。
 *
 * 分组口径（§4.4 ③）：`expires_at` 落在主窗口内 → primary；次窗口内 →
 * secondary；更远 → later；未上报（非 WorkBuddy 渠道或 expires_at 缺失）→
 * unreported（行内注明原因）。主窗口=0 时窗口内无行，全部已上报账号落 later。
 */
export function expiryRowsFor(
  accounts: AccountRow[],
  windows: { primarySeconds: number; secondarySeconds: number; nowMs: number },
): ExpiryRow[] {
  const nowSeconds = Math.floor(windows.nowMs / 1000)
  const primaryEnd = nowSeconds + Math.max(0, windows.primarySeconds)
  const secondaryEnd = nowSeconds + Math.max(0, windows.secondarySeconds)

  const rows = accounts.map((account): ExpiryRow => {
    const quota = account.quota
    const expiresAt = Number(quota?.expires_at || 0)
    const reported = Number.isFinite(expiresAt) && expiresAt > 0
    let group: ExpiryGroupKey = 'unreported'
    if (reported) {
      if (windows.primarySeconds <= 0) {
        // 到期排序已关闭（主窗口=0，次窗口同存 0）：不做窗口比较，全部归「更远」。
        group = 'later'
      } else if (expiresAt <= primaryEnd) group = 'primary'
      else if (expiresAt <= secondaryEnd) group = 'secondary'
      else group = 'later'
    }
    return {
      id: account.id,
      name: account.name || account.id,
      provider: String(account.provider || '').toLowerCase(),
      region: account.region || '',
      expiresAt: reported ? expiresAt : 0,
      expiringRemain: Number(quota?.expiring_remain || 0),
      unit: quota?.unit || '',
      packages: quota?.packages ?? [],
      group,
    }
  })

  rows.sort((left, right) => {
    const byGroup = EXPIRY_GROUP_ORDER.indexOf(left.group) - EXPIRY_GROUP_ORDER.indexOf(right.group)
    if (byGroup !== 0) return byGroup
    if (left.group === 'unreported') return left.name.localeCompare(right.name)
    if (left.expiresAt !== right.expiresAt) return left.expiresAt - right.expiresAt
    return left.name.localeCompare(right.name)
  })
  return rows
}

/** 分组视图（保持组内顺序）；用于渲染四个分组区块。 */
export function groupExpiryRows(rows: ExpiryRow[]): Record<ExpiryGroupKey, ExpiryRow[]> {
  const groups: Record<ExpiryGroupKey, ExpiryRow[]> = {
    primary: [],
    secondary: [],
    later: [],
    unreported: [],
  }
  for (const row of rows) groups[row.group].push(row)
  return groups
}

/** 按单位分别求和：不同渠道的单位不同（credits / 次），不能跨单位相加。 */
export function sumByUnit(rows: ExpiryRow[]): UnitTotal[] {
  const totals = new Map<string, number>()
  for (const row of rows) {
    if (!(row.expiringRemain > 0)) continue
    totals.set(row.unit, (totals.get(row.unit) || 0) + row.expiringRemain)
  }
  return [...totals.entries()]
    .map(([unit, amount]) => ({ unit, amount }))
    .sort((left, right) => right.amount - left.amount || left.unit.localeCompare(right.unit))
}

export function summarizeExpiry(rows: ExpiryRow[]): ExpirySummary {
  const groups = groupExpiryRows(rows)
  return {
    primary: { count: groups.primary.length, totals: sumByUnit(groups.primary) },
    secondary: { count: groups.secondary.length, totals: sumByUnit(groups.secondary) },
    later: { count: groups.later.length, totals: sumByUnit(groups.later) },
    unreported: { count: groups.unreported.length },
  }
}

/** 渠道键固定序（批次 15：渠道 × 区域组合）：其余按名称排在最后。 */
export const EXPIRY_CHANNEL_ORDER = ['workbuddy-cn', 'workbuddy-global', 'trae-cn']

/** 单渠道到期分布（概览栏的「按渠道」行）。 */
export type ExpiryChannelSummary = {
  /** 渠道键（`<provider>-<region>`，region 缺省退化为 provider）。 */
  channel: string
  /** 该渠道的账号数。 */
  accountCount: number
  summary: ExpirySummary
}

/** 按渠道键分组汇总（批次 10 起；15 起键为渠道 × 区域组合；展示名由视图层映射）。 */
export function summarizeExpiryByChannel(rows: ExpiryRow[]): ExpiryChannelSummary[] {
  const bucket = new Map<string, ExpiryRow[]>()
  for (const row of rows) {
    const key = accountChannelKey(row.provider, row.region)
    const list = bucket.get(key)
    if (list) list.push(row)
    else bucket.set(key, [row])
  }
  const rank = (key: string) => {
    const index = EXPIRY_CHANNEL_ORDER.indexOf(key)
    return index === -1 ? EXPIRY_CHANNEL_ORDER.length : index
  }
  return [...bucket.entries()]
    .sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
    .map(([channel, items]) => ({
      channel,
      accountCount: items.length,
      summary: summarizeExpiry(items),
    }))
}

export function relativeExpiryLabel(expiresAt: number, nowMs: number, t: Translate): string {
  const seconds = expiresAt - Math.floor(nowMs / 1000)
  if (seconds <= 0) return t('expiryRelativeExpired')
  if (seconds < 3600) return t('expiryRelativeMinutes', { n: Math.max(1, Math.round(seconds / 60)) })
  if (seconds < SECONDS_PER_DAY) return t('expiryRelativeHours', { n: Math.max(1, Math.round(seconds / 3600)) })
  return t('expiryRelativeDays', { n: Math.max(1, Math.round(seconds / SECONDS_PER_DAY)) })
}

/** 绝对时刻（服务器下发 Unix 秒 → 本地时区字符串）。 */
export function absoluteExpiryLabel(expiresAt: number): string {
  if (!expiresAt) return ''
  const date = new Date(expiresAt * 1000)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString()
}
