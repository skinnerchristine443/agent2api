// @vitest-environment happy-dom
import { act, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/hooks/I18nProvider'

vi.mock('@/api/system', () => ({
  updateSystemSettings: vi.fn(async (input: Record<string, unknown>) => ({
    activity_report_enabled: false,
    activity_report_time: '09:00',
    ...input,
  })),
}))

import { ActivityReportCard } from './ActivityReportCard'
import { updateSystemSettings } from '@/api/system'

function renderCard(settings: Record<string, unknown> | null) {
  return render(
    <I18nProvider>
      <ActivityReportCard settings={settings as never} onSaved={() => {}} />
    </I18nProvider>,
  )
}

describe('ActivityReportCard（对话活跃上报）', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('标题与说明渲染', () => {
    renderCard({ activity_report_enabled: false, activity_report_time: '09:00' })
    expect(screen.getByText('对话活跃上报')).toBeTruthy()
    expect(screen.getByLabelText('开启活跃上报')).toBeTruthy()
  })

  it('关闭时时刻输入禁用（无意义）', () => {
    renderCard({ activity_report_enabled: false, activity_report_time: '09:00' })
    const time = screen.getByLabelText('上报时刻') as HTMLInputElement
    expect(time.disabled).toBe(true)
  })

  it('开启后时刻输入可用，且缺省回落到 09:00', () => {
    renderCard({ activity_report_enabled: true, activity_report_time: '' })
    const time = screen.getByLabelText('上报时刻') as HTMLInputElement
    expect(time.disabled).toBe(false)
    expect(time.value).toBe('09:00')
  })

  it('切换开关即 PATCH activity_report_enabled', async () => {
    renderCard({ activity_report_enabled: false, activity_report_time: '09:00' })
    const toggle = screen.getByLabelText('开启活跃上报')
    await act(async () => {
      toggle.click()
    })
    expect(updateSystemSettings).toHaveBeenCalledWith({ activity_report_enabled: true })
  })
})
