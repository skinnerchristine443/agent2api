// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { PageHeader } from './PageHeader'

describe('PageHeader', () => {
  it('渲染描述与主操作，且不含页面级标题（与 AppHeader 分工）', () => {
    render(<PageHeader description="按最近 7 天聚合" actions={<button type="button">导出</button>} />)
    expect(screen.getByText('按最近 7 天聚合')).toBeTruthy()
    expect(screen.getByRole('button', { name: '导出' })).toBeTruthy()
    expect(screen.queryByRole('heading')).toBeNull()
  })
})
