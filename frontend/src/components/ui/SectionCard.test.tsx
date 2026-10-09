// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { SectionCard } from './SectionCard'

describe('SectionCard', () => {
  it('渲染标题 / 说明 / 右侧插槽 / 内容区', () => {
    render(
      <SectionCard title="账号概览" hint="统计窗口：最近 24 小时" right={<button type="button">刷新</button>}>
        <p>内容区</p>
      </SectionCard>,
    )
    expect(screen.getByText('账号概览')).toBeTruthy()
    expect(screen.getByText('统计窗口：最近 24 小时')).toBeTruthy()
    expect(screen.getByRole('button', { name: '刷新' })).toBeTruthy()
    expect(screen.getByText('内容区')).toBeTruthy()
  })

  it('padded=false 时不额外加内边距容器', () => {
    const { container } = render(
      <SectionCard title="明细" padded={false}>
        <table />
      </SectionCard>,
    )
    expect(container.querySelector('section > div:last-child')?.getAttribute('class')).toBeNull()
  })
})
