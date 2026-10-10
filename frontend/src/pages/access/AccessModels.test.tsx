// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyProvider'
import { I18nProvider } from '@/hooks/I18nProvider'
import { OverviewProvider } from '@/hooks/OverviewProvider'

import { AccessModels } from './AccessModels'

// 渲染冒烟：数据层整体 mock（api 层是页面唯一的值导入点），验证页面
// 挂载 → 取数 → 表格/卡片与筛选控件落地，不涉及真实网络。
vi.mock('@/api/overview', () => ({
  fetchOverviewSummary: vi.fn(async () => ({})),
  fetchModelsCached: vi.fn(async () => ({
    data: [
      { id: 'glm-5.3', display_name: 'GLM 5.3', provider: 'workbuddy', region: 'global', context_length: 200000 },
      { id: 'solo-coder', display_name: 'Solo Coder', provider: 'trae', region: 'cn', supports_max_mode: true },
    ],
  })),
  fetchProviders: vi.fn(async () => ({
    data: [{
      id: 'workbuddy',
      label: 'WorkBuddy',
      runtime: 'in_process',
      capabilities: { browser_login: true, pat_login: true, import_export: true },
      regions: [{ id: 'global', label: '国际版' }],
      default_region: 'global',
    }],
  })),
  refreshModels: vi.fn(async () => ({ data: [] })),
  updateProviderMaxMode: vi.fn(async () => ({})),
  updateProviderReasoning: vi.fn(async () => ({})),
}))

function renderPage(entry = '/access') {
  return render(
    <I18nProvider>
      <ApiKeyProvider>
        <OverviewProvider>
          <MemoryRouter initialEntries={[entry]}>
            <AccessModels />
          </MemoryRouter>
        </OverviewProvider>
      </ApiKeyProvider>
    </I18nProvider>,
  )
}

describe('AccessModels 渲染冒烟', () => {
  it('取数完成后渲染模型行（表格 + 移动端卡片）与筛选控件', async () => {
    renderPage()
    expect((await screen.findAllByText('GLM 5.3')).length).toBeGreaterThan(0)
    expect(screen.getAllByText('Solo Coder').length).toBeGreaterThan(0)
    expect(screen.getAllByLabelText('筛选').length).toBeGreaterThan(0)
    expect(screen.getAllByLabelText('供应商').length).toBeGreaterThan(0)
  })

  it('直链筛选生效：?q=solo 只保留命中模型', async () => {
    renderPage('/access?q=solo&provider=trae:cn')
    expect((await screen.findAllByText('Solo Coder')).length).toBeGreaterThan(0)
    expect(screen.queryByText('GLM 5.3')).toBeNull()
  })
})
