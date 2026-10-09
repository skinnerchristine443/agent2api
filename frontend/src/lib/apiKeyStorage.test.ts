import { beforeEach, describe, expect, it, vi } from 'vitest'
import { API_KEY_STORAGE_KEY, readStoredApiKey } from './apiKeyStorage'

// node 环境没有 localStorage；用最小 stub 注入，每个用例获得独立存储。
describe('readStoredApiKey', () => {
  beforeEach(() => {
    const store = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => {
        store.set(key, value)
      },
      removeItem: (key: string) => {
        store.delete(key)
      },
    })
  })

  it('returns an empty string when nothing is stored', () => {
    expect(readStoredApiKey()).toBe('')
  })

  it('trims the stored value', () => {
    localStorage.setItem(API_KEY_STORAGE_KEY, '  secret-key  ')
    expect(readStoredApiKey()).toBe('secret-key')
  })

  it('treats a whitespace-only value as absent', () => {
    localStorage.setItem(API_KEY_STORAGE_KEY, '   ')
    expect(readStoredApiKey()).toBe('')
  })
})
