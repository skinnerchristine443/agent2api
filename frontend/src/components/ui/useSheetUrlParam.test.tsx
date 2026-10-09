// @vitest-environment happy-dom
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigationType } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { useSheetUrlParam } from './useSheetUrlParam'

// 探针把 hook 的返回值与 URL 现状全部渲染成可断言的文本，测试只通过
// 真实点击驱动（不捕获 hook 返回对象、不绕过 React 渲染）。
function Probe() {
  const sheet = useSheetUrlParam('request')
  const location = useLocation()
  const navigationType = useNavigationType()
  return (
    <div>
      <span data-testid="open">{sheet.isOpen ? sheet.value : '未打开'}</span>
      <span data-testid="search">{location.search}</span>
      <span data-testid="nav">{navigationType}</span>
      <button type="button" onClick={() => sheet.open('req-2')}>打开</button>
      <button type="button" onClick={() => sheet.onOpenChange(false)}>关闭</button>
    </div>
  )
}

function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Probe />
    </MemoryRouter>,
  )
}

describe('useSheetUrlParam', () => {
  it('直达 URL 带参即打开（首帧进 POP，可分享链接）', () => {
    renderAt('/logs/requests?page=2&request=req-1')
    expect(screen.getByTestId('open').textContent).toBe('req-1')
    expect(screen.getByTestId('nav').textContent).toBe('POP')
  })

  it('打开 push 写参并保留其余参数；关闭（onOpenChange）replace 删参', () => {
    renderAt('/logs/requests?page=2')
    expect(screen.getByTestId('open').textContent).toBe('未打开')

    fireEvent.click(screen.getByRole('button', { name: '打开' }))
    expect(screen.getByTestId('open').textContent).toBe('req-2')
    expect(screen.getByTestId('search').textContent).toBe('?page=2&request=req-2')
    expect(screen.getByTestId('nav').textContent).toBe('PUSH')

    // Sheet 的关闭动作（Esc / 遮罩 / 关闭按钮）统一走 onOpenChange(false)
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.getByTestId('open').textContent).toBe('未打开')
    expect(screen.getByTestId('search').textContent).toBe('?page=2')
    expect(screen.getByTestId('nav').textContent).toBe('REPLACE')
  })
})
