import { describe, expect, it } from 'vitest'
import type { Translate } from '../i18n/messages'
import {
  accountChannelKey,
  accountChannelLabel,
  accountChannelLabelByKey,
  accountProviderLabel,
  isTraeProvider,
  isWorkBuddyProvider,
} from './provider'

// 回显 key，让断言钉住所请求的精确译文。
const t = ((key: string) => `t:${key}`) as unknown as Translate

describe('provider predicates', () => {
  it('matches WorkBuddy, Trae and empty case-insensitively and exactly', () => {
    expect(isWorkBuddyProvider('WorkBuddy')).toBe(true)
    expect(isWorkBuddyProvider('workbuddy-cn')).toBe(false)
    expect(isTraeProvider('TRAE')).toBe(true)
    expect(isTraeProvider('trae-cn')).toBe(false)
    expect(isWorkBuddyProvider(undefined)).toBe(false)
    expect(isTraeProvider('')).toBe(false)
  })
})

describe('accountChannelKey / accountChannelLabel（批次 15：渠道 × 区域五组合）', () => {
  it('键 = `<provider>-<region>`；region 缺省退化为 provider', () => {
    expect(accountChannelKey('workbuddy', 'cn')).toBe('workbuddy-cn')
    expect(accountChannelKey('Trae', 'CN')).toBe('trae-cn')
    expect(accountChannelKey('trae')).toBe('trae')
    expect(accountChannelKey()).toBe('')
  })

  it('展示名：已知渠道品牌化 + 区域后缀；未知渠道原样', () => {
    expect(accountChannelLabel('workbuddy', 'cn')).toBe('WorkBuddy CN')
    expect(accountChannelLabel('workbuddy', 'global')).toBe('WorkBuddy Global')
    expect(accountChannelLabel('trae', 'cn')).toBe('Trae CN')
    expect(accountChannelLabel('mystery')).toBe('mystery')
  })

  it('键 → 展示名（组合键 / 裸键 / 未知键）', () => {
    expect(accountChannelLabelByKey('workbuddy-global')).toBe('WorkBuddy Global')
    expect(accountChannelLabelByKey('trae-cn')).toBe('Trae CN')
    expect(accountChannelLabelByKey('trae')).toBe('Trae')
    expect(accountChannelLabelByKey('mystery-cn')).toBe('mystery CN')
  })
})

describe('accountProviderLabel', () => {
  it('splits WorkBuddy by region', () => {
    expect(accountProviderLabel('workbuddy', 'global', t)).toBe('t:accountTypeWorkBuddyGlobal')
    expect(accountProviderLabel('workbuddy', 'cn', t)).toBe('t:accountTypeWorkBuddyCN')
    expect(accountProviderLabel('workbuddy', undefined, t)).toBe('t:accountTypeWorkBuddyCN')
  })

  it('has a single Trae region label', () => {
    expect(accountProviderLabel('trae', 'cn', t)).toBe('t:accountTypeTraeCN')
  })

  it('returns the raw value for an unknown provider', () => {
    expect(accountProviderLabel('mystery', 'cn', t)).toBe('mystery')
  })
})
