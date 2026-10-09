import { describe, expect, it } from 'vitest'

import {
  customRangeFromISO,
  dateValueToISO,
  formatCredit,
  formatLatency,
  isoToDateValue,
  pickEnum,
  rangeFromPreset,
  reasoningLabel,
  statusColor,
  ERROR_KIND_VALUES,
  RUNTIME_LEVEL_VALUES,
} from './logsFormat'

// 纯函数与 URL 取值域（node 环境，无 DOM）：重点是「自定义区间 ↔ URL from/to」
// 的往返（DateRangePicker 的 minute 精度）与非法取值的回落，避免手改 URL
// 或时区/精度差异把筛选打进非法态。

describe('logsFormat 取值域', () => {
  it('pickEnum 命中白名单、非法 / 缺失回落 undefined', () => {
    expect(pickEnum('rate_limit', ERROR_KIND_VALUES)).toBe('rate_limit')
    expect(pickEnum('error', RUNTIME_LEVEL_VALUES)).toBe('error')
    expect(pickEnum('nope', ERROR_KIND_VALUES)).toBeUndefined()
    expect(pickEnum('', RUNTIME_LEVEL_VALUES)).toBeUndefined()
    expect(pickEnum(null, RUNTIME_LEVEL_VALUES)).toBeUndefined()
  })
})

describe('logsFormat 时间区间', () => {
  it('预设映射相对「此刻」的 from/to（窗口上限 = now，长度正确）', () => {
    const now = Date.now()
    const hour = rangeFromPreset('1h')
    expect(hour.to).toBeTruthy()
    expect(new Date(hour.to as string).getTime()).toBeGreaterThanOrEqual(now - 1000)
    expect(new Date(hour.to as string).getTime() - new Date(hour.from as string).getTime()).toBe(60 * 60 * 1000)

    const week = rangeFromPreset('7d')
    expect(new Date(week.to as string).getTime() - new Date(week.from as string).getTime()).toBe(7 * 24 * 60 * 60 * 1000)
  })

  it('all / custom 不带区间（custom 由 URL 的 from/to 提供）', () => {
    expect(rangeFromPreset('all')).toEqual({ from: undefined, to: undefined })
    expect(rangeFromPreset('custom')).toEqual({ from: undefined, to: undefined })
  })

  it('ISO ↔ DateValue 往返稳定：秒 / 毫秒被截到分钟，不随往返漂移', () => {
    const iso = '2026-10-07T03:24:00.000Z'
    const value = isoToDateValue(iso)
    expect(value).not.toBeNull()
    expect(dateValueToISO(value)).toBe(iso)
    // 结束时刻取整分末（区间右闭），再次往返仍稳定
    const end = dateValueToISO(value, true)
    expect(end).toBe('2026-10-07T03:24:59.999Z')
    expect(dateValueToISO(isoToDateValue(end), true)).toBe(end)
  })

  it('非法 / 缺失 ISO 一律返回 null（不抛错）', () => {
    expect(isoToDateValue('')).toBeNull()
    expect(isoToDateValue('not-a-date')).toBeNull()
    expect(isoToDateValue(undefined)).toBeNull()
    expect(dateValueToISO(null)).toBeUndefined()
  })

  it('customRangeFromISO 要求两端齐全（缺一即视为未选择）', () => {
    expect(customRangeFromISO('2026-10-07T03:24:00.000Z', '2026-10-07T05:00:00.000Z')).not.toBeNull()
    expect(customRangeFromISO('2026-10-07T03:24:00.000Z', '')).toBeNull()
    expect(customRangeFromISO(undefined, undefined)).toBeNull()
  })
})

describe('logsFormat 展示口径', () => {
  it('statusColor / formatLatency / formatCredit', () => {
    expect(statusColor('ok')).toBe('success')
    expect(statusColor('incomplete')).toBe('warning')
    expect(statusColor('canceled')).toBe('danger')
    expect(statusColor('unknown')).toBe('default')
    expect(formatLatency(undefined)).toBe('—')
    expect(formatLatency(420)).toBe('420ms')
    expect(formatLatency(1500)).toBe('1.5s')
    expect(formatCredit(1.234567)).toBe('1.2346')
    expect(formatCredit(null)).toBe('—')
  })

  it('reasoningLabel：相同不重复、不同显示请求 → 实际', () => {
    expect(reasoningLabel({})).toBe('')
    expect(reasoningLabel({ resolved_reasoning: 'high' })).toBe('high')
    expect(reasoningLabel({ requested_reasoning: 'high' })).toBe('high')
    expect(reasoningLabel({ requested_reasoning: 'low', resolved_reasoning: 'high' })).toBe('low → high')
  })
})
