// @vitest-environment happy-dom
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/hooks/I18nProvider'
import { NotFoundPage } from './NotFoundPage'

// 栈深 2：无论 goBack 走 history 回退还是无历史回退首页，终点都是 '/'。
function renderAt(path: string) {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={['/', path]}>
        <Routes>
          <Route path="/" element={<div>overview-marker</div>} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>,
  )
}

describe('NotFoundPage', () => {
  it('未知路径渲染 404（而不是静默重定向）', () => {
    renderAt('/does-not-exist')
    expect(screen.getByText('页面不存在')).toBeTruthy()
    expect(screen.getByText(/链接过期或输入有误/)).toBeTruthy()
  })

  it('「回到概览」跳转到首页', () => {
    renderAt('/does-not-exist')
    fireEvent.click(screen.getByText('回到概览'))
    expect(screen.getByText('overview-marker')).toBeTruthy()
  })

  it('「返回上一页」回到上一项（无历史时回退概览）', () => {
    renderAt('/does-not-exist')
    fireEvent.click(screen.getByText('返回上一页'))
    expect(screen.getByText('overview-marker')).toBeTruthy()
  })
})
