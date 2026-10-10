// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/hooks/I18nContext'

import { GrowthOverview } from './GrowthOverview'
import { translate } from '@/i18n/messages'

const t = translate

function renderOverview(rows: Parameters<typeof GrowthOverview>[0]['rows']) {
  return render(
    <I18nProvider>
      <GrowthOverview
        rows={rows}
        loading={false}
        error={null}
        onReload={() => {}}
        onClaimAll={() => {}}
        claimAllPending={false}
        claimAllResult={null}
        t={t}
      />
    </I18nProvider>,
  )
}

// 总览必须给出「合计 已领 X / 可领 Y」这一行——这正是「一个个账号去查太麻烦」
// 要解决的诉求；失败行不得计入合计。
describe('GrowthOverview', () => {
  it('渲染合计与逐账号进度，失败行不计入合计', () => {
    renderOverview([
      { account_id: 'a', name: '账号A', provider: 'workbuddy', region: 'cn', claimed: 17, claimable: 2, total: 19 },
      { account_id: 'b', name: '账号B', provider: 'workbuddy', region: 'cn', claimed: 10, claimable: 0, total: 10 },
      { account_id: 'c', name: '账号C', provider: 'workbuddy', region: 'cn', claimed: 99, claimable: 99, total: 99, error: 'upstream 503' },
    ])
    expect(screen.getByText('合计 已领 27 / 可领 2')).toBeTruthy()
    expect(screen.getByText('17 / 19')).toBeTruthy()
    expect(screen.getByText('10 / 10')).toBeTruthy()
    expect(screen.getByText('读取失败')).toBeTruthy()
    // 失败行的 99/99 不得进入合计。
    expect(screen.queryByText('合计 已领 126 / 可领 101')).toBeNull()
  })

  it('无可领账号时一键领取按钮禁用', () => {
    renderOverview([
      { account_id: 'a', name: '账号A', provider: 'workbuddy', region: 'cn', claimed: 18, claimable: 0, total: 18 },
    ])
    const button = screen.getByRole('button', { name: '一键领取全部可领' })
    expect((button as HTMLButtonElement).disabled).toBe(true)
  })

  it('有可领账号时一键领取按钮可用', () => {
    renderOverview([
      { account_id: 'a', name: '账号A', provider: 'workbuddy', region: 'cn', claimed: 17, claimable: 1, total: 18 },
    ])
    const button = screen.getByRole('button', { name: '一键领取全部可领' })
    expect((button as HTMLButtonElement).disabled).toBe(false)
  })
})
