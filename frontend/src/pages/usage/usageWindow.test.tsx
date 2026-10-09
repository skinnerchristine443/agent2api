// @vitest-environment happy-dom
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, useSearchParams } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { translate } from '@/i18n/messages'
import { navPageFor } from '@/nav/nav'

import { DEFAULT_USAGE_WINDOW, parseUsageWindow, usageWindowDays, usageWindowLabelKey } from './usageWindow'
import { useUsageWindow } from './useUsageWindow'

describe('usageWindow（纯函数）', () => {
  it('URL 取值域 1d / 7d / 30d → days=1 / 7 / 30', () => {
    expect(parseUsageWindow('1d')).toBe('1d')
    expect(parseUsageWindow('7d')).toBe('7d')
    expect(parseUsageWindow('30d')).toBe('30d')
    expect(usageWindowDays('1d')).toBe(1)
    expect(usageWindowDays('7d')).toBe(7)
    expect(usageWindowDays('30d')).toBe(30)
  })

  it('非法 / 缺失取值回退默认 7d', () => {
    expect(parseUsageWindow(null)).toBe(DEFAULT_USAGE_WINDOW)
    expect(parseUsageWindow('')).toBe(DEFAULT_USAGE_WINDOW)
    expect(parseUsageWindow('14d')).toBe(DEFAULT_USAGE_WINDOW)
    expect(parseUsageWindow('7')).toBe(DEFAULT_USAGE_WINDOW)
    expect(parseUsageWindow('1D')).toBe(DEFAULT_USAGE_WINDOW)
  })

  it('窗口文案 key 一一对应', () => {
    expect(usageWindowLabelKey('1d')).toBe('usageWindow1d')
    expect(translate(usageWindowLabelKey('1d'))).toBe('1 天')
    expect(translate(usageWindowLabelKey('7d'))).toBe('7 天')
    expect(translate(usageWindowLabelKey('30d'))).toBe('30 天')
  })
})

function WindowHarness() {
  const { window, days, setWindow } = useUsageWindow()
  const [params] = useSearchParams()
  return (
    <div>
      <span data-testid="window">{window}</span>
      <span data-testid="days">{days}</span>
      <span data-testid="search">{params.toString()}</span>
      <button type="button" onClick={() => setWindow('1d')}>切 1 天</button>
      <button type="button" onClick={() => setWindow('30d')}>切 30 天</button>
      <button type="button" onClick={() => setWindow('7d')}>切 7 天</button>
    </div>
  )
}

function renderHarness(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <WindowHarness />
    </MemoryRouter>,
  )
}

describe('用量窗口 URL 化', () => {
  it('直链读取 ?window=30d → days=30', () => {
    renderHarness('/usage?window=30d')
    expect(screen.getByTestId('window').textContent).toBe('30d')
    expect(screen.getByTestId('days').textContent).toBe('30')
  })

  it('非法 window 回退默认：?window=14d → 7d', () => {
    renderHarness('/usage?window=14d')
    expect(screen.getByTestId('window').textContent).toBe('7d')
    expect(screen.getByTestId('days').textContent).toBe('7')
  })

  it('切换窗口写 URL（replace）；回到默认 7d 时删除参数', () => {
    renderHarness('/usage')
    expect(screen.getByTestId('search').textContent).toBe('')

    fireEvent.click(screen.getByRole('button', { name: '切 1 天' }))
    expect(screen.getByTestId('window').textContent).toBe('1d')
    expect(screen.getByTestId('search').textContent).toBe('window=1d')

    fireEvent.click(screen.getByRole('button', { name: '切 30 天' }))
    expect(screen.getByTestId('search').textContent).toBe('window=30d')

    fireEvent.click(screen.getByRole('button', { name: '切 7 天' }))
    expect(screen.getByTestId('window').textContent).toBe('7d')
    expect(screen.getByTestId('search').textContent).toBe('') // 默认值不写入 URL
  })
})

describe('D3 回归：/usage 页头标题', () => {
  it('标题由 nav 单一事实源推导为「用量统计」（菜单名 = 页面标题）', () => {
    const page = navPageFor('/usage')
    expect(page?.key).toBe('navUsage')
    expect(translate(page!.key)).toBe('用量统计')
  })
})
