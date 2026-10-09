import { describe, expect, it } from 'vitest'
import {
  formatBytes,
  formatCompact,
  formatCountKind,
  formatLatency,
  formatPercent,
  modelCreditsText,
  modelIsFree,
} from './format'

describe('formatCompact', () => {
  it('rounds plain counts below one thousand', () => {
    expect(formatCompact(0)).toBe('0')
    expect(formatCompact(999)).toBe('999')
  })

  it('switches to k and M at the documented thresholds', () => {
    expect(formatCompact(1000)).toBe('1k')
    expect(formatCompact(1500)).toBe('1.5k')
    expect(formatCompact(12345)).toBe('12k')
    expect(formatCompact(2_400_000)).toBe('2.4M')
  })

  it('keeps the sign', () => {
    expect(formatCompact(-1500)).toBe('-1.5k')
  })

  it('renders a dash for non-finite input instead of NaN', () => {
    expect(formatCompact(Number.NaN)).toBe('—')
    expect(formatCompact(Number.POSITIVE_INFINITY)).toBe('—')
  })
})

describe('formatLatency', () => {
  it('uses milliseconds below a second and seconds above', () => {
    expect(formatLatency(0)).toBe('0ms')
    expect(formatLatency(950)).toBe('950ms')
    expect(formatLatency(1000)).toBe('1s')
    expect(formatLatency(1500)).toBe('1.5s')
  })

  it('renders a dash for missing or non-finite input', () => {
    expect(formatLatency(null)).toBe('—')
    expect(formatLatency(undefined)).toBe('—')
    expect(formatLatency(Number.NaN)).toBe('—')
  })
})

describe('formatBytes', () => {
  it('scales by 1024 and stops at TB', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1023)).toBe('1023 B')
    expect(formatBytes(1024)).toBe('1 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(1024 ** 4)).toBe('1 TB')
  })

  it('rejects negative and non-finite input', () => {
    expect(formatBytes(-1)).toBe('—')
    expect(formatBytes(null)).toBe('—')
    expect(formatBytes(Number.NaN)).toBe('—')
  })
})

describe('formatPercent', () => {
  it('keeps one decimal below ten percent and none above', () => {
    expect(formatPercent(0.05)).toBe('5.0%')
    expect(formatPercent(0.1234)).toBe('12%')
    expect(formatPercent(1)).toBe('100%')
  })

  it('renders a dash rather than a bogus percentage', () => {
    expect(formatPercent(null)).toBe('—')
    expect(formatPercent(Number.NaN)).toBe('—')
  })
})

describe('formatCountKind', () => {
  it('routes each kind to its formatter', () => {
    expect(formatCountKind(1500, 'int')).toBe('1500')
    expect(formatCountKind(1500, 'compact')).toBe('1.5k')
    expect(formatCountKind(0.1234, 'percent')).toBe('12%')
    expect(formatCountKind(1500, 'ms')).toBe('1.5s')
  })
})

describe('modelIsFree', () => {
  it('trusts the explicit flag', () => {
    expect(modelIsFree({ free: true })).toBe(true)
  })

  it('treats a zero credit declaration as free', () => {
    expect(modelIsFree({ credits: '0' })).toBe(true)
    expect(modelIsFree({ credits: '0.0' })).toBe(true)
  })

  it('does not report a paid model as free', () => {
    expect(modelIsFree({ credits: '10' })).toBe(false)
    expect(modelIsFree({ credits: '' })).toBe(false)
    expect(modelIsFree({})).toBe(false)
    expect(modelIsFree({ free: false, credits: null })).toBe(false)
  })
})

describe('modelCreditsText', () => {
  it('trims the declared credits and normalises missing values', () => {
    expect(modelCreditsText({ credits: '  5 credits ' })).toBe('5 credits')
    expect(modelCreditsText({})).toBe('')
    expect(modelCreditsText({ credits: null })).toBe('')
  })
})
