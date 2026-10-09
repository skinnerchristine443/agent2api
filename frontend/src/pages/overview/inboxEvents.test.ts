import { describe, expect, it } from 'vitest'

import type { QuotaAlert } from '@/api/overview'
import { translate } from '@/i18n/messages'
import type { AccountRow } from '@/lib/account'

import { buildInboxEvents, EXPIRY_WINDOW_DAYS } from './inboxEvents'

const NOW = Date.parse('2026-10-09T10:00:00+08:00')

function account(overrides: Partial<AccountRow> & { id: string }): AccountRow {
  return {
    provider: 'workbuddy',
    region: 'global',
    name: overrides.id,
    enabled: true,
    ...overrides,
  } as AccountRow
}

function alert(overrides: Partial<QuotaAlert> & { account_id: string }): QuotaAlert {
  return { category: 'quota_exceeded', message: 'quota exceeded', ...overrides }
}

function build(accounts: AccountRow[], alerts: QuotaAlert[] = []) {
  return buildInboxEvents({ accounts, alerts, t: translate, now: NOW })
}

describe('收件箱聚合（inboxEvents）', () => {
  it('限额告警：exceeded → danger、low → warning，跳「需关注」并带 focus', () => {
    const events = build(
      [account({ id: 'a1', name: '主号' })],
      [
        alert({ account_id: 'a1', account_name: '主号', category: 'quota_exceeded' }),
        alert({ account_id: 'a1', account_name: '主号', category: 'quota_low' }),
      ],
    )
    expect(events).toHaveLength(2)
    const [exceeded, low] = events
    expect(exceeded.severity).toBe('danger')
    expect(exceeded.detail).toContain('额度已耗尽')
    expect(exceeded.to).toBe('/accounts?quick=attn&provider=workbuddy-global&focus=a1')
    expect(low.severity).toBe('warning')
    expect(low.detail).toContain('预留底线')
  })

  it('登录失效：auth_failed / login_required / dead 均入箱（danger）', () => {
    const events = build([
      account({ id: 'a1', status: 'auth_failed' }),
      account({ id: 'a2', status: 'login_required' }),
      account({ id: 'a3', status: 'dead' }),
    ])
    expect(events.map((event) => event.kind)).toEqual(['authFailed', 'authFailed', 'authFailed'])
    expect(events.every((event) => event.severity === 'danger')).toBe(true)
    expect(events[0].to).toBe('/accounts?quick=attn&provider=workbuddy-global&focus=a1')
  })

  it('签到失败：danger + 携带错误消息 + 跳账号池（渠道页签对齐）并带 focus + HH:mm 时间', () => {
    const events = build([
      account({
        id: 'a1',
        last_checkin_status: 'error',
        last_checkin_msg: '网络超时',
        last_checkin_at: '2026-10-09T08:20:00+08:00',
      }),
      account({ id: 'tr-1', provider: 'trae', region: 'cn', last_checkin_status: 'error' }),
    ])
    expect(events).toHaveLength(2)
    const checkinEvent = events.find((event) => event.accountId === 'a1')!
    expect(checkinEvent.kind).toBe('checkinFailed')
    expect(checkinEvent.severity).toBe('danger')
    expect(checkinEvent.detail).toContain('网络超时')
    expect(checkinEvent.meta).toBe('08:20')
    expect(checkinEvent.to).toBe('/accounts?provider=workbuddy-global&focus=a1')
    // 非默认渠道账号：跳转链接必须落到其所在渠道页签（否则页签过滤后目标行不可见）。
    expect(events.find((event) => event.accountId === 'tr-1')?.to).toBe('/accounts?provider=trae-cn&focus=tr-1')
  })

  it('冷却：warning + 剩余时长标签 + 跳「在途」视图', () => {
    const events = build([
      account({ id: 'a1', status: 'cooling', down_until: new Date(NOW + 12 * 60 * 1000).toISOString() }),
    ])
    expect(events).toHaveLength(1)
    expect(events[0].kind).toBe('cooling')
    expect(events[0].severity).toBe('warning')
    expect(events[0].meta).toBe('12m')
    expect(events[0].to).toBe('/accounts?quick=transit&provider=workbuddy-global&focus=a1')
  })

  it('额度将到期：≤3 天 danger、4–7 天 warning、跳额度页；窗口外不入箱', () => {
    const days = (count: number) => Math.floor((NOW + count * 24 * 60 * 60 * 1000) / 1000)
    const events = build([
      account({ id: 'a1', quota: { expires_at: days(3), expiring_remain: 1200, unit: 'credits' } }),
      account({ id: 'a2', quota: { expires_at: days(5), expiring_remain: 800, unit: 'credits' } }),
      account({ id: 'a3', quota: { expires_at: days(EXPIRY_WINDOW_DAYS + 1), expiring_remain: 500, unit: 'credits' } }),
      account({ id: 'a4', quota: { expires_at: days(2), expiring_remain: 0, unit: 'credits' } }),
    ])
    expect(events.map((event) => event.accountId).sort()).toEqual(['a1', 'a2'])
    const danger = events.find((event) => event.accountId === 'a1')
    const warn = events.find((event) => event.accountId === 'a2')
    expect(danger?.severity).toBe('danger')
    expect(danger?.meta).toBe('3 天后')
    expect(danger?.to).toBe('/quota?account=a1')
    expect(warn?.severity).toBe('warning')
  })

  it('排序：danger 先于 warning；danger 内按事件时间倒序', () => {
    const events = build(
      [account({ id: 'a1', last_checkin_status: 'error', last_checkin_at: '2026-10-09T08:00:00+08:00' })],
      [alert({ account_id: 'a2', account_name: '二号' })],
    )
    // a1 签到失败（at=08:00）与 a2 限额告警（at=0）：同为 danger，有时间者在前
    expect(events.map((event) => event.accountId)).toEqual(['a1', 'a2'])
  })

  it('空输入 → 空数组', () => {
    expect(build([])).toEqual([])
  })
})
