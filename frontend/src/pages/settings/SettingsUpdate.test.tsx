// @vitest-environment happy-dom
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyProvider'
import { I18nProvider } from '@/hooks/I18nProvider'
import { API_KEY_STORAGE_KEY } from '@/lib/apiKeyStorage'

const api = vi.hoisted(() => ({
  fetchSystemUpdate: vi.fn(),
  fetchSystemSettings: vi.fn(),
  updateSystemSettings: vi.fn(),
  startSystemUpdate: vi.fn(),
  applyPreparedSystemUpdate: vi.fn(),
  cancelSystemUpdate: vi.fn(),
  rollbackSystemUpdate: vi.fn(),
}))
vi.mock('@/api/system', () => api)

import { SettingsUpdate } from './SettingsUpdate'

function renderPage() {
  return render(
    <I18nProvider>
      <ApiKeyProvider>
        <MemoryRouter initialEntries={['/system/update']}>
          <SettingsUpdate />
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

describe('SettingsUpdate 版本更新', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem(API_KEY_STORAGE_KEY, 'console-key')
    api.fetchSystemUpdate.mockReset().mockResolvedValue({
      current_version: '1.2.3',
      next_version: '1.2.4',
      has_update: true,
      managed: false,
      cached: false,
      agent: { available: false, state: 'unavailable' },
      rollback_versions: [],
      recent_releases: [],
    })
  })

  it('前置诊断列出「不可更新」的两类原因与修复指引', async () => {
    renderPage()
    await flush()
    expect(screen.getByText('这台机器暂时不能更新')).toBeTruthy()
    expect(screen.getByText('当前运行的必须是已发布的 GitHub Release，不能是本地或开发镜像。')).toBeTruthy()
    expect(screen.getByText('用 deploy/install-updater.sh 安装宿主机更新器，让它能调用 Docker。')).toBeTruthy()
    expect(screen.getByText(/更新器 socket 挂进容器/)).toBeTruthy()
    // 不能更新时主操作禁用，仅保留检查更新
    expect(screen.getByText('下载更新').closest('button')?.disabled).toBe(true)
  })

  it('「检查更新」用 ?force 强制刷新（绕过 10 分钟缓存）', async () => {
    renderPage()
    await flush()
    expect(api.fetchSystemUpdate).toHaveBeenCalledWith(false)

    fireEvent.click(screen.getByText('检查更新'))
    await flush()
    expect(api.fetchSystemUpdate).toHaveBeenCalledWith(true)
  })

  it('可更新且已就绪时给出立即更新入口与版本历史', async () => {
    api.fetchSystemUpdate.mockResolvedValue({
      current_version: '1.2.3',
      next_version: '1.2.5',
      skipped_versions: ['v1.2.4'],
      has_update: true,
      managed: true,
      cached: false,
      agent: { available: true, staged_update: true, state: 'ready_to_apply', current_version: '1.2.3', target_version: 'v1.2.5' },
      update: { job_id: 'job-1', state: 'ready_to_apply', target_version: 'v1.2.5' },
      rollback_versions: [{ tag_name: 'v1.2.2' }],
      recent_releases: [{ tag_name: 'v1.2.5', body: 'notes' }],
    })
    renderPage()
    await flush()

    expect(screen.getByText('这台机器可以更新')).toBeTruthy()
    expect(screen.getByRole('button', { name: '立即更新' })).toBeTruthy()
    // 跨版本提示「中间经过的版本」
    expect(screen.getByText('中间经过的版本')).toBeTruthy()
    expect(screen.getByText('v1.2.4')).toBeTruthy()
    // 版本历史里有可回滚项
    expect(screen.getByText('历史版本')).toBeTruthy()
  })
})
