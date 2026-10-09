// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Sheet } from './Sheet'

describe('Sheet', () => {
  it('isOpen=false 不渲染内容，isOpen=true 渲染标题与正文', () => {
    const { rerender } = render(
      <Sheet isOpen={false} onOpenChange={() => {}} title="请求详情">
        <p>详情正文</p>
      </Sheet>,
    )
    expect(screen.queryByText('请求详情')).toBeNull()

    rerender(
      <Sheet isOpen onOpenChange={() => {}} title="请求详情">
        <p>详情正文</p>
      </Sheet>,
    )
    expect(screen.getByText('请求详情')).toBeTruthy()
    expect(screen.getByText('详情正文')).toBeTruthy()
    expect(screen.getByRole('button', { name: '关闭' })).toBeTruthy()
  })
})
