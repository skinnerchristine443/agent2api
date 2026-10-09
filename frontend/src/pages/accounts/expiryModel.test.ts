import { describe, expect, it } from 'vitest'

import type { AccountRow } from '@/lib/account'
import type { Translate } from '@/i18n/messages'

import {
  absoluteExpiryLabel,
  daysToSeconds,
  expiryRowsFor,
  groupExpiryRows,
  normalizeWindowSeconds,
  parseWindowDays,
  relativeExpiryLabel,
  secondsToDaysInput,
  summarizeExpiry,
  summarizeExpiryByChannel,
  sumByUnit,
} from './expiryModel'

const DAY = 24 * 60 * 60
const NOW_MS = 1_000_000_000_000 // 固定基准，避免时区/时间漂移
const NOW_S = NOW_MS / 1000

function account(id: string, quota?: AccountRow['quota'], extra: Partial<AccountRow> = {}): AccountRow {
  return { id, name: `账号 ${id}`, provider: 'workbuddy', region: 'cn', ...extra, quota }
}

const WINDOWS = { primarySeconds: 3 * DAY, secondarySeconds: 7 * DAY, nowMs: NOW_MS }

describe('expiryModel · 窗口天数换算', () => {
  it('secondsToDaysInput 保留非整天值（打开页面不静默取整）', () => {
    expect(secondsToDaysInput(0)).toBe('0')
    expect(secondsToDaysInput(-5)).toBe('0')
    expect(secondsToDaysInput(Number.NaN)).toBe('0')
    expect(secondsToDaysInput(3 * DAY)).toBe('3')
    expect(secondsToDaysInput(36 * 3600)).toBe('1.5')
  })

  it('parseWindowDays 只接受非负有限数', () => {
    expect(parseWindowDays(' 3 ')).toBe(3)
    expect(parseWindowDays('0')).toBe(0)
    expect(parseWindowDays('1.5')).toBe(1.5)
    expect(parseWindowDays('')).toBeNull()
    expect(parseWindowDays('abc')).toBeNull()
    expect(parseWindowDays('-1')).toBeNull()
  })

  it('daysToSeconds 取整到秒；主窗口=0 时次窗口同存为 0', () => {
    expect(daysToSeconds(3)).toBe(3 * DAY)
    expect(daysToSeconds(0.0001)).toBe(9)
    expect(normalizeWindowSeconds(0, 7 * DAY)).toEqual({ primarySeconds: 0, secondarySeconds: 0 })
    expect(normalizeWindowSeconds(3 * DAY, 7 * DAY)).toEqual({ primarySeconds: 3 * DAY, secondarySeconds: 7 * DAY })
  })
})

describe('expiryModel · 明细分组与排序', () => {
  const accounts: AccountRow[] = [
    account('a-later', { expires_at: NOW_S + 30 * DAY, expiring_remain: 10, unit: 'credits' }),
    account('b-secondary', { expires_at: NOW_S + 5 * DAY, expiring_remain: 20, unit: 'credits' }),
    account('c-primary', { expires_at: NOW_S + 1 * DAY, expiring_remain: 100, unit: 'credits' }),
    account('d-expired', { expires_at: NOW_S - 60, expiring_remain: 5, unit: 'credits' }),
    account('e-unreported'),
    account('f-noexpiry', { expiring_remain: 7, unit: 'credits' }),
  ]

  it('按窗口四组归类：主窗口内（含已过期）/ 次窗口内 / 更远 / 未上报', () => {
    const rows = expiryRowsFor(accounts, WINDOWS)
    const byId = new Map(rows.map((row) => [row.id, row.group]))
    expect(byId.get('c-primary')).toBe('primary')
    expect(byId.get('d-expired')).toBe('primary')
    expect(byId.get('b-secondary')).toBe('secondary')
    expect(byId.get('a-later')).toBe('later')
    expect(byId.get('e-unreported')).toBe('unreported')
    expect(byId.get('f-noexpiry')).toBe('unreported')
  })

  it('组序固定、组内按到期时刻升序、未上报按名称排序', () => {
    const rows = expiryRowsFor(accounts, WINDOWS)
    expect(rows.map((row) => row.id)).toEqual([
      'd-expired',
      'c-primary',
      'b-secondary',
      'a-later',
      'e-unreported',
      'f-noexpiry',
    ])
  })

  it('未上报行保留账号信息但到期字段归零', () => {
    const rows = expiryRowsFor(accounts, WINDOWS)
    const unreported = rows.find((row) => row.id === 'e-unreported')
    expect(unreported?.expiresAt).toBe(0)
    expect(unreported?.expiringRemain).toBe(0)
    expect(unreported?.packages).toEqual([])
  })

  it('主窗口=0（到期排序关闭）：已上报账号全部落「更远」', () => {
    const rows = expiryRowsFor(accounts, { primarySeconds: 0, secondarySeconds: 0, nowMs: NOW_MS })
    const groups = groupExpiryRows(rows)
    expect(groups.primary).toEqual([])
    expect(groups.secondary).toEqual([])
    expect(groups.later.map((row) => row.id)).toEqual(['d-expired', 'c-primary', 'b-secondary', 'a-later'])
    expect(groups.unreported).toHaveLength(2)
  })
})

describe('expiryModel · 概览聚合', () => {
  it('按单位分别求和（不同渠道单位不可相加）', () => {
    const rows = expiryRowsFor([
      account('a', { expires_at: NOW_S + DAY, expiring_remain: 100, unit: 'credits' }),
      account('b', { expires_at: NOW_S + 2 * DAY, expiring_remain: 250, unit: 'credits' }),
      account('c', { expires_at: NOW_S + DAY, expiring_remain: 5, unit: '次' }),
    ], WINDOWS)
    expect(sumByUnit(rows)).toEqual([
      { unit: 'credits', amount: 350 },
      { unit: '次', amount: 5 },
    ])
  })

  it('summarizeExpiry 给出四组计数与主/次窗口额度合计', () => {
    const rows = expiryRowsFor([
      account('a', { expires_at: NOW_S + DAY, expiring_remain: 100, unit: 'credits' }),
      account('b', { expires_at: NOW_S + 5 * DAY, expiring_remain: 20, unit: 'credits' }),
      account('c', { expires_at: NOW_S + 30 * DAY }),
      account('d'),
    ], WINDOWS)
    const summary = summarizeExpiry(rows)
    expect(summary.primary.count).toBe(1)
    expect(summary.primary.totals).toEqual([{ unit: 'credits', amount: 100 }])
    expect(summary.secondary.count).toBe(1)
    expect(summary.secondary.totals).toEqual([{ unit: 'credits', amount: 20 }])
    expect(summary.later.count).toBe(1)
    expect(summary.unreported.count).toBe(1)
  })

  it('summarizeExpiryByChannel：按「渠道 × 区域」分组、固定序且未知渠道排最后', () => {
    const rows = expiryRowsFor([
      account('a1', { expires_at: NOW_S + DAY, expiring_remain: 100, unit: 'credits' }, { provider: 'Acme' }),
      account('w1', { expires_at: NOW_S + DAY, expiring_remain: 50, unit: 'credits' }),
      account('w2', { expires_at: NOW_S + 5 * DAY, expiring_remain: 20, unit: 'credits' }, { region: 'global' }),
      account('t1', { expires_at: NOW_S + 30 * DAY, expiring_remain: 30, unit: '次' }, { provider: 'trae' }),
      account('t2', undefined, { provider: 'trae' }),
      account('x1', { expires_at: NOW_S + 2 * DAY, expiring_remain: 10, unit: 'credits' }, { provider: 'zeta' }),
    ], WINDOWS)
    const channels = summarizeExpiryByChannel(rows)
    // 固定序：workbuddy-cn → workbuddy-global → trae-cn；未知键排最后。
    expect(channels.map((item) => item.channel)).toEqual(['workbuddy-cn', 'workbuddy-global', 'trae-cn', 'acme-cn', 'zeta-cn'])
    expect(channels[0]).toMatchObject({ channel: 'workbuddy-cn', accountCount: 1 })
    expect(channels[0].summary.primary.totals).toEqual([{ unit: 'credits', amount: 50 }])
    expect(channels[1]).toMatchObject({ channel: 'workbuddy-global', accountCount: 1 })
    expect(channels[1].summary.secondary.totals).toEqual([{ unit: 'credits', amount: 20 }])
    expect(channels[2]).toMatchObject({ channel: 'trae-cn', accountCount: 2 })
    expect(channels[2].summary.later.totals).toEqual([{ unit: '次', amount: 30 }])
    expect(channels[2].summary.unreported.count).toBe(1)
    // provider 大小写已归一（'Acme' → 'acme-cn'）且与默认 region（cn）组合。
    expect(channels[3]).toMatchObject({ channel: 'acme-cn', accountCount: 1 })
    expect(channels[3].summary.primary.totals).toEqual([{ unit: 'credits', amount: 100 }])
  })
})

describe('expiryModel · 时刻文案', () => {
  const t = ((key: string, vars?: Record<string, string | number>) => (
    vars?.n != null ? `${key}:${vars.n}` : key
  )) as Translate

  it('相对时刻按分钟 / 小时 / 天分档，过期与未知分别呈现', () => {
    expect(relativeExpiryLabel(NOW_S - 10, NOW_MS, t)).toBe('expiryRelativeExpired')
    expect(relativeExpiryLabel(NOW_S + 30 * 60, NOW_MS, t)).toBe('expiryRelativeMinutes:30')
    expect(relativeExpiryLabel(NOW_S + 5 * 3600, NOW_MS, t)).toBe('expiryRelativeHours:5')
    expect(relativeExpiryLabel(NOW_S + 2 * DAY, NOW_MS, t)).toBe('expiryRelativeDays:2')
  })

  it('绝对时刻：0 / 非法值返回空串', () => {
    expect(absoluteExpiryLabel(0)).toBe('')
    expect(absoluteExpiryLabel(NOW_S).length).toBeGreaterThan(0)
  })
})
