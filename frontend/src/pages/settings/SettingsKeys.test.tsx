// @vitest-environment happy-dom
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiKeyProvider } from '@/hooks/ApiKeyContext'
import { I18nProvider } from '@/hooks/I18nContext'
import { API_KEY_STORAGE_KEY } from '@/lib/apiKeyStorage'

const api = vi.hoisted(() => ({
  fetchConsoleKey: vi.fn(),
  rotateConsoleKey: vi.fn(),
  rotateProxyKey: vi.fn(),
  revealKey: vi.fn(),
}))
vi.mock('@/api/keys', () => api)

import { SettingsKeys } from './SettingsKeys'

function renderPage() {
  return render(
    <I18nProvider>
      <ApiKeyProvider>
        <MemoryRouter initialEntries={['/system/keys']}>
          <SettingsKeys />
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

describe('SettingsKeys 密钥轮换', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem(API_KEY_STORAGE_KEY, 'old-key')
    api.fetchConsoleKey.mockReset().mockResolvedValue({ prefix: 'ak_old…0001', target: 'console' })
    api.rotateConsoleKey.mockReset().mockResolvedValue({ prefix: 'ak_new…9999', target: 'console', rotated: true, secret: 'ak_new_secret' })
    api.rotateProxyKey.mockReset().mockResolvedValue({ prefix: 'px_new…8888', target: 'proxy', rotated: true, secret: 'px_new_secret' })
    api.revealKey.mockReset().mockImplementation(async (target: string) => (
      target === 'proxy'
        ? { prefix: 'px_ful…8888', target: 'proxy', secret: 'px_full_secret' }
        : { prefix: 'ak_old…0001', target: 'console', secret: 'ak_full_secret' }
    ))
  })

  it('控制台：二次确认 → 原子写回 → 显式确认前不得关闭', async () => {
    renderPage()
    await flush()
    expect(screen.getByText('ak_old…0001')).toBeTruthy()

    fireEvent.click(screen.getByText('轮换密钥'))
    await flush()
    // ④ 二次确认：先看到失效范围，不点确认不轮换
    expect(within(screen.getByRole('alertdialog')).getByText(/其他控制台会话需要重新登录/)).toBeTruthy()
    expect(api.rotateConsoleKey).not.toHaveBeenCalled()

    fireEvent.click(screen.getByText('立即轮换'))
    await flush()
    // ① 原子写回：本地钥已换成新值，明文可见；确认框已收起
    expect(localStorage.getItem(API_KEY_STORAGE_KEY)).toBe('ak_new_secret')
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(screen.getByText('新的控制台密钥')).toBeTruthy()
    expect(screen.getByText('ak_new_secret')).toBeTruthy()

    // ③ 显式保存确认门：Esc / 遮罩都不关闭
    fireEvent.keyDown(document.body, { key: 'Escape' })
    await flush()
    expect(screen.getByText('ak_new_secret')).toBeTruthy()

    fireEvent.click(screen.getByText('我已保存新钥'))
    await flush()
    expect(screen.queryByText('ak_new_secret')).toBeNull()
    expect(screen.getByText('ak_new…9999')).toBeTruthy() // 指纹已刷新
  })

  it('调用密钥：命中 proxy 目标，不覆盖控制台指纹、不写回管理面钥', async () => {
    renderPage()
    await flush()

    fireEvent.click(screen.getByText('轮换调用密钥'))
    await flush()
    expect(within(screen.getByRole('alertdialog')).getByText(/旧钥会立即失效/)).toBeTruthy()

    fireEvent.click(screen.getByText('立即轮换'))
    await flush()

    expect(api.rotateProxyKey).toHaveBeenCalledTimes(1)
    expect(api.rotateConsoleKey).not.toHaveBeenCalled()
    expect(screen.getByText('新的调用密钥')).toBeTruthy()
    expect(screen.getByText('px_new_secret')).toBeTruthy()
    expect(localStorage.getItem(API_KEY_STORAGE_KEY)).toBe('old-key') // 数据面钥不覆盖控制台会话
    expect(screen.getByText('ak_old…0001')).toBeTruthy() // 指纹保持不变

    fireEvent.click(screen.getByText('我已保存新钥'))
    await flush()
  })

  it('双钥语义与定位边界常驻页面', async () => {
    renderPage()
    await flush()
    expect(screen.getByText(/下一次登录/)).toBeTruthy()
    expect(screen.getByText(/只用于 \/v1\/\* 数据面/)).toBeTruthy()
    expect(screen.getByText(/不新增密钥、不做分组、也不按客户端分发/)).toBeTruthy()
  })

  it('显示完整密钥（批次 7）：reveal 后可见可复制，隐藏后回指纹；两卡各自独立', async () => {
    renderPage()
    await flush()
    // 初始：只有指纹与占位，无复制按钮。
    expect(screen.getByText('ak_old…0001')).toBeTruthy()
    expect(screen.getByText('••••••••••••••••')).toBeTruthy()
    expect(screen.queryAllByLabelText('复制').length).toBe(0)

    // 控制台卡：显示完整密钥。
    fireEvent.click(screen.getAllByText('显示完整密钥')[0])
    await flush()
    expect(api.revealKey).toHaveBeenCalledWith('console')
    expect(screen.getByText('ak_full_secret')).toBeTruthy()
    expect(screen.getAllByLabelText('复制').length).toBeGreaterThan(0)

    // 隐藏：回指纹，复制按钮随之消失。
    fireEvent.click(screen.getAllByLabelText('隐藏')[0])
    await flush()
    expect(screen.queryByText('ak_full_secret')).toBeNull()
    expect(screen.getByText('ak_old…0001')).toBeTruthy()
    expect(screen.queryAllByLabelText('复制').length).toBe(0)

    // 调用密钥卡（第二张）：独立 reveal，不影响控制台指纹。
    fireEvent.click(screen.getAllByText('显示完整密钥')[1])
    await flush()
    expect(api.revealKey).toHaveBeenLastCalledWith('proxy')
    expect(screen.getByText('px_full_secret')).toBeTruthy()
    expect(screen.getByText('ak_old…0001')).toBeTruthy()
  })
})
