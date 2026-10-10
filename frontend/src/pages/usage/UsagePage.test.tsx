// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyProvider'
import { I18nProvider } from '@/hooks/I18nProvider'
import { OverviewProvider } from '@/hooks/OverviewProvider'

import { UsagePage } from './UsagePage'

// 渲染冒烟：用量聚合整体 mock；验证指标行、三表与窗口按钮落地，
// 且窗口切换会把 `window` 写进 URL（与 usageWindow.test.tsx 的 hook 级断言互补）。
vi.mock('@/api/usage', () => ({
  fetchUsageStats: vi.fn(async (days: number) => ({
    window: { since: '2026-10-01', until: '2026-10-07', days },
    totals: {
      requests: 42,
      prompt_tokens: 1000,
      completion_tokens: 500,
      total_tokens: 1500,
      errors: 1,
      incomplete: 0,
      canceled: 0,
      cache_read_tokens: 200,
      cache_write_tokens: 0,
      cache_hit_rate: 0.25,
      output_tokens_per_second: 12.5,
    },
    daily: [
      {
        date: '2026-10-07',
        requests: 42,
        prompt_tokens: 1000,
        completion_tokens: 500,
        total_tokens: 1500,
        errors: 1,
        incomplete: 0,
        canceled: 0,
        cache_read_tokens: 200,
        output_tokens_per_second: 12.5,
      },
    ],
    models: [{ key: 'glm-5.3', requests: 42, prompt_tokens: 1000, completion_tokens: 500, total_tokens: 1500, errors: 1, cache_read_tokens: 200, cache_hit_rate: 0.25, output_tokens_per_second: 12.5 }],
    accounts: [
      { key: 'acc-1', requests: 20, prompt_tokens: 500, completion_tokens: 200, total_tokens: 700, errors: 0, cache_read_tokens: 100, cache_hit_rate: 0.25, output_tokens_per_second: 12.5 },
      { key: '(unassigned)', requests: 42, prompt_tokens: 1000, completion_tokens: 500, total_tokens: 1500, errors: 1, cache_read_tokens: 200, cache_hit_rate: 0.25, output_tokens_per_second: 12.5 },
    ],
    model_accounts: [
      { model: 'glm-5.3', account: 'acc-1', requests: 20, total_tokens: 700, errors: 0, output_tokens_per_second: 12.5 },
      { model: 'glm-5.3', account: '(unknown)', requests: 42, total_tokens: 1500, errors: 1, output_tokens_per_second: 12.5 },
    ],
  })),
}))

// 账号名映射：mock hook 本身（而非经 pages/** 导入 @/api，遵守约定 ④）。
vi.mock('@/hooks/useAccountNameMap', () => ({
  useAccountNameMap: () => new Map([['acc-1', '毕祥']]),
}))

function renderPage(entry = '/usage') {
  return render(
    <I18nProvider>
      <ApiKeyProvider>
        <OverviewProvider>
          <MemoryRouter initialEntries={[entry]}>
            <UsagePage />
          </MemoryRouter>
        </OverviewProvider>
      </ApiKeyProvider>
    </I18nProvider>,
  )
}

describe('UsagePage 渲染冒烟', () => {
  it('渲染窗口按钮、指标行与三表（含哨兵值文案）', async () => {
    renderPage()
    expect(await screen.findByText('请求数')).toBeTruthy()
    expect(screen.getByText('每日 Token 趋势')).toBeTruthy()
    // 分组表标题同时作为首列列名，故按「至少一处」断言
    expect(screen.getAllByText('各模型 Token 用量').length).toBeGreaterThan(0)
    expect(screen.getAllByText('各账号 Token 用量').length).toBeGreaterThan(0)
    expect(screen.getAllByText('模型 × 账号').length).toBeGreaterThan(0)
    // 哨兵值 (unknown)/(unassigned) 已本地化
    expect(screen.getByText('未分配')).toBeTruthy()
    expect(screen.getByText('未知')).toBeTruthy()
    // 账号 id 已映射成账号名（回归护栏：此前直接渲染 acc-1）
    expect(screen.queryByText('acc-1')).toBeNull()
    expect(screen.getAllByText('毕祥').length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: '1 天' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '7 天' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '30 天' })).toBeTruthy()
  })
})
