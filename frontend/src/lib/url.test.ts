import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { absUrl, originBase } from './url'

const originalLocation = (globalThis as { location?: unknown }).location

beforeEach(() => {
  // 该辅助函数读取真实的浏览器 `location`；node 测试进程没有它，
  // 所以先固定一个，之后再还原。
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { protocol: 'https:', host: 'gateway.example.com' },
  })
})

afterEach(() => {
  if (originalLocation === undefined) {
    delete (globalThis as { location?: unknown }).location
    return
  }
  Object.defineProperty(globalThis, 'location', { configurable: true, value: originalLocation })
})

describe('originBase', () => {
  it('joins the page protocol and host', () => {
    expect(originBase()).toBe('https://gateway.example.com')
  })
})

describe('absUrl', () => {
  it('falls back to the origin when nothing is given', () => {
    expect(absUrl()).toBe('https://gateway.example.com')
    expect(absUrl(null)).toBe('https://gateway.example.com')
    expect(absUrl('')).toBe('https://gateway.example.com')
  })

  it('leaves absolute http(s) URLs untouched', () => {
    expect(absUrl('https://api.other.com/v1/chat')).toBe('https://api.other.com/v1/chat')
    expect(absUrl('http://localhost:3010/v1')).toBe('http://localhost:3010/v1')
  })

  it('resolves root-relative and bare paths against the origin', () => {
    expect(absUrl('/v1/chat/completions')).toBe('https://gateway.example.com/v1/chat/completions')
    expect(absUrl('v1/models')).toBe('https://gateway.example.com/v1/models')
  })
})
