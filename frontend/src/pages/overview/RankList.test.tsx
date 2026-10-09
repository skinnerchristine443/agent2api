// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { RankList } from './RankList'

// 榜单项跳转（收口批）：带 `to` 的条目整行渲染为链接，无 `to` 保持纯文本行。
describe('RankList（榜单项跳转）', () => {
  it('带 to 的条目渲染为链接（href 精确）', () => {
    render(
      <MemoryRouter>
        <RankList
          items={[
            { key: 'quota', label: '额度', count: 3, to: '/logs/requests?status=error&kind=quota' },
          ]}
          empty="空"
        />
      </MemoryRouter>,
    )
    const link = screen.getByRole('link', { name: /额度/ })
    expect(link.getAttribute('href')).toBe('/logs/requests?status=error&kind=quota')
  })

  it('无 to 的条目不是链接（保持纯文本行）', () => {
    render(
      <MemoryRouter>
        <RankList items={[{ key: 'x', label: '纯文本项', count: 1 }]} empty="空" />
      </MemoryRouter>,
    )
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.getByText('纯文本项')).toBeTruthy()
  })
})
