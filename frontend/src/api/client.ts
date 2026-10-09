import { readStoredApiKey } from '@/lib/apiKeyStorage'
import { ApiError, isUnauthorized } from '@/lib/apiError'

// 重导出保持既有引用点不变（hooks / components 继续从 `@/api/client` 导入）；
// 页面层（pages/**）请从 `@/lib/apiError` 导入（护栏④：pages 零 @/api 值导入）。
export { ApiError, isUnauthorized }

export async function api<T = any>(path: string, opts: RequestInit = {}, keyOverride?: string): Promise<T> {
  const headers = new Headers(opts.headers || {})
  if (opts.body && !(opts.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const key = (keyOverride ?? readStoredApiKey()).trim()
  if (key && !headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${key}`)
  }
  const res = await fetch(path, { ...opts, headers })
  const text = await res.text()
  let data: any
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    const raw = typeof data === 'string' ? data : text
    const looksHTML = /<!doctype html|<html[\s>]/i.test(raw)
    const msg = data?.error?.message || data?.message || (looksHTML ? `${res.status} ${res.statusText}` : (raw || res.statusText))
    throw new ApiError(msg, res.status, data?.error?.code)
  }
  return data as T
}
