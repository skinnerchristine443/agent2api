// @vitest-environment happy-dom
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

// 测试环境冒烟（T51 前置）：验证 happy-dom + @testing-library/react 栈可用。
// 此前 vitest include 只收 `.test.ts`，`.test.tsx` 永不执行（假绿）；
// 本文件是首个真实执行的组件测试。
describe('组件测试环境', () => {
  it('可渲染组件并触发事件', () => {
    const onClick = vi.fn()
    render(<button onClick={onClick}>你好</button>)
    fireEvent.click(screen.getByRole('button', { name: '你好' }))
    expect(onClick).toHaveBeenCalledTimes(1)
  })
})
