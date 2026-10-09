// @vitest-environment happy-dom
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyContext'
import { I18nProvider } from '@/hooks/I18nContext'
import { OverviewProvider } from '@/hooks/OverviewContext'

import { AccessPage } from './AccessPage'

// 渲染冒烟（批次 6 页签化）：默认「连接信息」页签；切页签后调试台 / 模型目录
// 各自落地；curl 随账号钉死而带上 X-Agent2API-Account 头（纯函数接线是否正确）。
vi.mock('@/api/overview', () => ({
  fetchOverviewSummary: vi.fn(async () => ({})),
  fetchAccounts: vi.fn(async () => ({
    data: [{ id: 'acc-1', name: '主账号', provider: 'workbuddy', region: 'global', ready: true, enabled: true }],
  })),
  fetchModelsCached: vi.fn(async () => ({ data: [{ id: 'glm-5.3', display_name: 'GLM 5.3', provider: 'workbuddy' }] })),
  fetchProviders: vi.fn(async () => ({ data: [] })),
  testChat: vi.fn(async () => ({ id: 'chatcmpl-1' })),
}))

function renderPage(entry = '/access') {
  return render(
    <I18nProvider>
      <ApiKeyProvider>
        <OverviewProvider>
          <MemoryRouter initialEntries={[entry]}>
            <AccessPage />
          </MemoryRouter>
        </OverviewProvider>
      </ApiKeyProvider>
    </I18nProvider>,
  )
}

describe('AccessPage 页签（批次 6）', () => {
  it('默认「连接信息」页签：连接区落地、调试台不渲染', async () => {
    renderPage()
    expect(await screen.findByText('调用密钥状态')).toBeTruthy()
    expect(screen.getByRole('tab', { name: '连接信息' }).getAttribute('aria-selected')).toBe('true')
    expect(screen.queryByText('请求编排')).toBeNull()
  })

  it('切到「调试台」：请求编排 / 响应检查 / curl 落地，且默认不带账号头', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('tab', { name: '调试台' }))
    expect(await screen.findByText('请求编排')).toBeTruthy()
    expect(screen.getByText('响应检查')).toBeTruthy()
    expect(screen.getByText('curl 示例')).toBeTruthy()
    // 默认自动路由：curl 不带账号头
    expect(screen.getByText(/curl -sS/).textContent || '').not.toContain('X-Agent2API-Account')
  })

  it('`?tab=models` 直达模型目录页签（连接区不渲染）', async () => {
    renderPage('/access?tab=models')
    expect((await screen.findAllByText('GLM 5.3')).length).toBeGreaterThan(0)
    expect(screen.queryByText('调用密钥状态')).toBeNull()
  })
})
