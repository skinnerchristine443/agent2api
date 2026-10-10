// @vitest-environment happy-dom
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyContext'
import { I18nProvider } from '@/hooks/I18nContext'

import { GrowthPage } from './GrowthPage'

// 成长选择器的静态能力过滤（批次 6）：只列 `capabilities.growth` 的渠道账号；
// 无参数 / 参数指向不支持渠道时自动补选到支持渠道（replace 语义）。
//
// 约定护栏：pages/** 不得值导入 `@/api`——mock 工厂经 `vi.hoisted` 暴露，
// 测试体内直接操作该句柄，不 import 真模块。

const statusCalls: string[] = []

const apiMocks = vi.hoisted(() => ({
  fetchAccounts: vi.fn(async () => ({
    data: [
      { id: 'tr-1', name: 'Trae 号', provider: 'trae', region: 'cn', ready: true, enabled: true },
      { id: 'wb-1', name: 'WB 主号', provider: 'workbuddy', region: 'cn', ready: true, enabled: true },
    ],
  })),
  fetchProviders: vi.fn(async () => ({
    data: [
      {
        id: 'trae', label: 'Trae', runtime: 'in_process', default_region: 'cn', regions: [],
        capabilities: { browser_login: true, pat_login: false, import_export: true, growth: false },
      },
      {
        id: 'workbuddy', label: 'WorkBuddy', runtime: 'in_process', default_region: 'cn', regions: [],
        capabilities: { browser_login: true, pat_login: false, import_export: true, growth: true },
      },
    ],
  })),
}))

vi.mock('@/api/overview', () => ({
  fetchAccounts: apiMocks.fetchAccounts,
  fetchProviders: apiMocks.fetchProviders,
}))

vi.mock('@/api/growth', () => ({
  fetchGrowthStatus: vi.fn(async (accountId: string) => {
    statusCalls.push(accountId)
    throw new Error('test-stop')
  }),
  fetchGrowthObservations: vi.fn(async () => ({ data: [] })),
  claimGrowthRewards: vi.fn(async () => ({ outcomes: [] })),
  // 总览：两行，一好一坏——坏行必须带出原因且不拖垮整表。
  fetchGrowthOverview: vi.fn(async () => ({
    rows: [
      { account_id: 'wb-1', name: 'WB 主号', provider: 'workbuddy', region: 'cn', claimed: 17, claimable: 1, total: 18 },
      { account_id: 'wb-2', name: 'WB 二号', provider: 'workbuddy', region: 'cn', claimed: 0, claimable: 0, total: 0, error: 'upstream 503' },
    ],
  })),
  isGrowthUnavailable: () => false,
  isProviderUnsupported: () => false,
}))

function renderPage(entry = '/tasks') {
  return render(
    <I18nProvider>
      <ApiKeyProvider>
        <MemoryRouter initialEntries={[entry]}>
          <GrowthPage />
        </MemoryRouter>
      </ApiKeyProvider>
    </I18nProvider>,
  )
}

describe('成长中心选择器能力过滤（批次 6）', () => {
  it('自动补选跳过不支持渠道：状态查询终态落在 workbuddy 账号', async () => {
    statusCalls.length = 0
    renderPage()
    await waitFor(() => {
      expect(statusCalls[statusCalls.length - 1]).toBe('wb-1')
    })
  })

  it('参数指向不支持渠道时自愈到支持渠道', async () => {
    statusCalls.length = 0
    renderPage('/tasks?account=tr-1')
    await waitFor(() => {
      expect(statusCalls[statusCalls.length - 1]).toBe('wb-1')
    })
  })

  it('全部渠道都不支持时给出专属空态（不是假空数据）', async () => {
    apiMocks.fetchProviders.mockResolvedValueOnce({
      data: [
        {
          id: 'trae', label: 'Trae', runtime: 'in_process', default_region: 'cn', regions: [],
          capabilities: { browser_login: true, pat_login: false, import_export: true, growth: false },
        },
        {
          id: 'workbuddy', label: 'WorkBuddy', runtime: 'in_process', default_region: 'cn', regions: [],
          capabilities: { browser_login: true, pat_login: false, import_export: true, growth: false },
        },
      ],
    })
    renderPage()
    expect(await screen.findByText('没有支持成长中心的账号')).toBeTruthy()
  })

  it('workbuddy 国际区域账号不在候选内（无成长任务领取机制）', async () => {
    statusCalls.length = 0
    apiMocks.fetchAccounts.mockResolvedValueOnce({
      data: [
        { id: 'wb-gl', name: 'WB Global 号', provider: 'workbuddy', region: 'global', ready: true, enabled: true },
        { id: 'wb-1', name: 'WB 主号', provider: 'workbuddy', region: 'cn', ready: true, enabled: true },
      ],
    })
    renderPage()
    await waitFor(() => {
      expect(statusCalls[statusCalls.length - 1]).toBe('wb-1')
    })
    expect(statusCalls).not.toContain('wb-gl')
  })
})
