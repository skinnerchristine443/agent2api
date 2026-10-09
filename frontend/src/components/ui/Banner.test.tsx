// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Banner } from './Banner'

describe('Banner', () => {
  it('渲染标题 / 说明 / 操作，并有 status 语义', () => {
    render(
      <Banner
        status="warning"
        title="有 2 个账号额度即将到期"
        description="建议在到期前重新登录以刷新凭证"
        actions={<button type="button">查看详情</button>}
      />,
    )
    expect(screen.getByRole('status')).toBeTruthy()
    expect(screen.getByText('有 2 个账号额度即将到期')).toBeTruthy()
    expect(screen.getByText('建议在到期前重新登录以刷新凭证')).toBeTruthy()
    expect(screen.getByRole('button', { name: '查看详情' })).toBeTruthy()
  })

  it('不含 dismiss 能力（无关闭按钮，方案 §6.5）', () => {
    render(<Banner status="danger" title="服务连接失败" />)
    expect(screen.queryByRole('button')).toBeNull()
  })
})
