// API 错误类型与判定（纯数据类 + 纯函数；由 `api/client.ts` 重导出以保持既有引用点不变）。
//
// 位置说明（收口批）：页面层需要 `isUnauthorized` 判定登录错误，而护栏④
// 「pages/** 对 @/api 值导入 = 0」不允许页面从 api 层取值——因此把它与
// ApiError 类一起放在 lib/（无状态工具与纯函数层）：api 层重导出（hooks /
// components 照旧从 `@/api/client` 导入），页面从 `@/lib/apiError` 导入。

export class ApiError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

/** 判定错误是否为鉴权失败（401 / invalid_api_key；含字符串回退，兼容非 ApiError 场景）。 */
export function isUnauthorized(err: unknown) {
  if (err instanceof ApiError) return err.status === 401 || err.code === 'invalid_api_key'
  const msg = err instanceof Error ? err.message : String(err || '')
  return /invalid_api_key|unauthorized|Missing\/invalid API key/i.test(msg)
}
