// @vitest-environment happy-dom
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { StatusDot } from './StatusDot'

describe('StatusDot', () => {
  it('携带 status-dot class 与 data-state，供既有 CSS 变量配色生效', () => {
    const { container } = render(<StatusDot state="warn" className="shrink-0" />)
    const dot = container.querySelector('.status-dot')
    expect(dot).toBeTruthy()
    expect(dot?.getAttribute('data-state')).toBe('warn')
    expect(dot?.getAttribute('class')).toContain('shrink-0')
    expect(dot?.getAttribute('aria-hidden')).toBe('true')
  })
})
