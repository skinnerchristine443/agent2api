// @vitest-environment happy-dom
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyContext'
import { I18nProvider } from '@/hooks/I18nContext'
import { OverviewProvider } from '@/hooks/OverviewContext'
import { API_KEY_STORAGE_KEY } from '@/lib/apiKeyStorage'

const overviewApi = vi.hoisted(() => ({
  fetchOverviewSummary: vi.fn(),
}))
vi.mock('@/api/overview', () => overviewApi)
// GSAP 揭幕动画与登录逻辑无关，测试环境静默。
vi.mock('@/hooks/useGsapReveal', () => ({ useGsapReveal: () => {} }))

import { LoginPage } from './LoginPage'

function renderPage() {
  return render(
    <I18nProvider>
        <ApiKeyProvider>
          <OverviewProvider>
            <MemoryRouter initialEntries={['/login']}>
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                <Route path="/" element={<div>overview-marker</div>} />
              </Routes>
            </MemoryRouter>
          </OverviewProvider>
        </ApiKeyProvider>
    </I18nProvider>,
  )
}

async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

describe('LoginPage 登录', () => {
  beforeEach(() => {
    localStorage.clear()
    overviewApi.fetchOverviewSummary.mockReset().mockResolvedValue({})
  })

  it('空密码提交显示校验提示且不发生跳转', async () => {
    renderPage()
    fireEvent.click(screen.getByText('进入'))
    await flush()
    expect(screen.getByText('密码是空的。')).toBeTruthy()
    expect(overviewApi.fetchOverviewSummary).not.toHaveBeenCalled()
    expect(screen.queryByText('overview-marker')).toBeNull()
  })

  it('有效密码：写回本地钥并进入概览', async () => {
    renderPage()
    fireEvent.change(screen.getByLabelText('控制台密码'), { target: { value: '  console-key-1  ' } })
    fireEvent.click(screen.getByText('进入'))
    await flush()
    expect(localStorage.getItem(API_KEY_STORAGE_KEY)).toBe('console-key-1')
    expect(overviewApi.fetchOverviewSummary).toHaveBeenCalledWith('console-key-1')
    expect(screen.getByText('overview-marker')).toBeTruthy()
  })
})
