import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  accountState,
  cooldownLabel,
  formatQuotaAmount,
  isAvailable,
  modelCooldownEntries,
  quotaTone,
  quotaUsedRatio,
  quotaWindows,
  runtimeTone,
  type AccountRow,
} from './account'

// 固定"现在"，使冷却/过期判定完全确定（原先这些纯函数零覆盖）。
const NOW = new Date('2026-10-07T00:00:00.000Z')

function row(partial: Partial<AccountRow>): AccountRow {
  return partial as unknown as AccountRow
}

afterEach(() => {
  vi.useRealTimers()
})

describe('cooldownLabel', () => {
  it('returns empty for missing, invalid, or elapsed deadlines', () => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    expect(cooldownLabel(undefined)).toBe('')
    expect(cooldownLabel(null)).toBe('')
    expect(cooldownLabel('not-a-date')).toBe('')
    expect(cooldownLabel('2026-10-06T23:59:59Z')).toBe('')
  })

  it('renders seconds below a minute and minutes above', () => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    expect(cooldownLabel('2026-10-07T00:00:30Z')).toBe('30s')
    expect(cooldownLabel('2026-10-07T00:01:30Z')).toBe('2m')
    expect(cooldownLabel('2026-10-07T02:00:00Z')).toBe('120m')
  })
})

describe('modelCooldownEntries', () => {
  it('drops elapsed entries and sorts by model id', () => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    const entries = modelCooldownEntries(
      row({
        model_cooldowns: {
          'glm-5.2': '2026-10-07T01:00:00Z',
          'deepseek-v4': '2026-10-07T00:30:00Z',
          expired: '2026-10-06T23:00:00Z',
        },
      }),
    )
    expect(entries.map(([model]) => model)).toEqual(['deepseek-v4', 'glm-5.2'])
  })
})

describe('accountState', () => {
  it('classifies the documented states', () => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    expect(accountState(row({ enabled: false }))).toBe('disabled')
    expect(accountState(row({ enabled: true, status: 'quota_exhausted' }))).toBe('quota_exhausted')
    expect(accountState(row({ enabled: true, quota: { exceeded: true } }))).toBe('quota_exhausted')
    expect(accountState(row({ enabled: true, down_until: '2026-10-07T00:05:00Z' }))).toBe('cooling')
    expect(accountState(row({ enabled: true, runtime_state: 'dead' }))).toBe('dead')
    expect(accountState(row({ enabled: true, status: 'auth_failed' }))).toBe('auth_failed')
    expect(accountState(row({ enabled: true, status: 'login_required' }))).toBe('login')
    // starting 且尚无配额 → loading；已带配额 → starting
    expect(accountState(row({ enabled: true, status: 'starting' }))).toBe('loading')
    expect(
      accountState(row({ enabled: true, status: 'starting', quota: {} })),
    ).toBe('starting')
    expect(accountState(row({ enabled: true, status: 'ready', hot: true }))).toBe('hot')
    expect(accountState(row({ enabled: true, status: 'ready' }))).toBe('ready')
    expect(accountState(row({ enabled: true, status: 'error' }))).toBe('unavailable')
    expect(accountState(row({ enabled: true }))).toBe('unavailable')
  })

  it('isAvailable only for hot and ready', () => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    expect(isAvailable(row({ enabled: true, status: 'ready', hot: true }))).toBe(true)
    expect(isAvailable(row({ enabled: true, status: 'ready' }))).toBe(true)
    expect(isAvailable(row({ enabled: true, status: 'cooling', down_until: '2026-10-07T00:05:00Z' }))).toBe(false)
    expect(isAvailable(row({ enabled: false }))).toBe(false)
  })

  it('routes runtime tone for the status palette', () => {
    expect(runtimeTone('hot')).toBe('ok')
    expect(runtimeTone('cooling')).toBe('warn')
    expect(runtimeTone('quota_exhausted')).toBe('warn')
    expect(runtimeTone('auth_failed')).toBe('danger')
    expect(runtimeTone('login')).toBe('danger')
  })
})

describe('quota helpers', () => {
  it('formats amounts with the k threshold', () => {
    expect(formatQuotaAmount(undefined)).toBe('—')
    expect(formatQuotaAmount(Number.NaN)).toBe('—')
    expect(formatQuotaAmount(3.14159)).toBe('3.14')
    expect(formatQuotaAmount(2000)).toBe('2k')
    expect(formatQuotaAmount(1234.5)).toBe('1.2k')
  })

  it('clamps the used ratio into [0, 1]', () => {
    expect(quotaUsedRatio({ percentage: 50 } as never)).toBe(0.5)
    expect(quotaUsedRatio({ percentage: 250 } as never)).toBe(1)
    expect(quotaUsedRatio({ percentage: -5 } as never)).toBe(0)
    expect(quotaUsedRatio({} as never)).toBe(0)
  })

  it('maps tones at the documented thresholds', () => {
    expect(quotaTone({ exceeded: true, percentage: 10 })).toBe('danger')
    expect(quotaTone({ exceeded: false, percentage: 80 })).toBe('warn')
    expect(quotaTone({ exceeded: false, percentage: 79 })).toBe('ok')
    expect(quotaTone({ exceeded: false })).toBe('ok')
  })

  it('keeps only windows with an id', () => {
    const windows = quotaWindows({
      windows: [{ id: 'daily' }, { id: '' }, { id: 'weekly' }],
    } as never)
    expect(windows.map((window) => window.id)).toEqual(['daily', 'weekly'])
  })
})
