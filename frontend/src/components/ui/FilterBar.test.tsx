// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { FilterBar } from './FilterBar'

describe('FilterBar', () => {
  it('渲染左侧筛选控件、右侧计数与操作', () => {
    render(
      <FilterBar count="显示 12 / 40 条" actions={<button type="button">清除筛选</button>}>
        <label>
          状态
          <input />
        </label>
      </FilterBar>,
    )
    expect(screen.getByLabelText('状态')).toBeTruthy()
    expect(screen.getByText('显示 12 / 40 条')).toBeTruthy()
    expect(screen.getByRole('button', { name: '清除筛选' })).toBeTruthy()
  })
})
