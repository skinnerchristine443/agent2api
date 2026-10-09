// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { KeyValue } from './KeyValue'

describe('KeyValue', () => {
  it('以 dl/dt/dd 语义渲染描述列表', () => {
    render(
      <KeyValue
        items={[
          { label: '请求 ID', value: 'req-20261007-0001' },
          { label: '耗时', value: '1.2s' },
        ]}
      />,
    )
    expect(screen.getAllByRole('term').map((node) => node.textContent)).toEqual(['请求 ID', '耗时'])
    expect(screen.getAllByRole('definition').map((node) => node.textContent)).toEqual(['req-20261007-0001', '1.2s'])
  })
})
