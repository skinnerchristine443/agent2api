// @vitest-environment happy-dom
import { act, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyContext'
import { I18nProvider } from '@/hooks/I18nContext'
import { API_KEY_STORAGE_KEY } from '@/lib/apiKeyStorage'

vi.mock('@/api/system', () => ({
  fetchSystemSettings: vi.fn(async () => ({
    cross_provider_model_pool: true,
    checkin_disabled_accounts: false,
    routing_strategy: 'round-robin',
    proxy_url: 'http://127.0.0.1:7890',
    session_affinity: { ttl_seconds: 600, hits: 3, misses: 1, escapes: 0 },
  })),
  updateSystemSettings: vi.fn(),
}))

vi.mock('@/api/overview', () => ({
  fetchProviders: vi.fn(async () => ({ data: [] })),
}))

import { SettingsParams } from './SettingsParams'

function renderPage() {
  return render(
    <I18nProvider>
      <ApiKeyProvider>
        <MemoryRouter initialEntries={['/system']}>
          <SettingsParams />
        </MemoryRouter>
      </ApiKeyProvider>
    </I18nProvider>,
  )
}

async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

describe('SettingsParams（运行参数页签）', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem(API_KEY_STORAGE_KEY, 'console-key')
  })

  it('保留的区块仍在：模型池 / 代理 / 路由策略', async () => {
    renderPage()
    await flush()
    expect(screen.getByText('跨 Provider 模型池')).toBeTruthy()
    expect(screen.getByText('统一代理出口')).toBeTruthy()
    expect(screen.getByText('账号调度策略')).toBeTruthy()
  })

  it('更新区与密钥区不再出现在本页；停用签到已迁至「签到」页签', async () => {
    renderPage()
    await flush()
    expect(screen.queryByText('检查更新')).toBeNull()
    expect(screen.queryByText('控制台 API 密钥')).toBeNull()
    expect(screen.queryByText('SQLite 保护')).toBeNull()
    expect(screen.queryByText('停用账号也自动签到')).toBeNull()
  })
})
