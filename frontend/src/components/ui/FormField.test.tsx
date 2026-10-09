// @vitest-environment happy-dom
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { FormField } from './FormField'

describe('FormField', () => {
  it('Label 与控件通过 htmlFor 关联，并渲染说明文字', () => {
    render(
      <FormField label="账号别名" htmlFor="alias" description="展示在账号卡片上">
        <input id="alias" />
      </FormField>,
    )
    expect(screen.getByLabelText('账号别名')).toBeTruthy()
    expect(screen.getByText('展示在账号卡片上')).toBeTruthy()
  })

  it('存在 error 时以 role=alert 呈现，并顶替 description', () => {
    render(
      <FormField label="账号别名" htmlFor="alias" description="展示在账号卡片上" error="别名不能为空">
        <input id="alias" />
      </FormField>,
    )
    expect(screen.getByRole('alert').textContent).toBe('别名不能为空')
    expect(screen.queryByText('展示在账号卡片上')).toBeNull()
  })
})
