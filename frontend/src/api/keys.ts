import { api } from './client'

/** 两把固定角色钥：console = 管理面（/api/*），proxy = 数据面（/v1/*）。 */
export type KeyTarget = 'console' | 'proxy'

export type ConsoleKeyView = {
  prefix: string
  hint?: string
  target?: KeyTarget
  rotated?: boolean
  secret?: string
}

/** 读取指纹：后端只支持 console（proxy 无读取路径，仅能轮换）。 */
export function fetchConsoleKey(signal?: AbortSignal) {
  return api<ConsoleKeyView>('/api/system/console-key', { signal })
}

/** 轮换并一次性返回明文；target 缺省 console（与后端缺省一致）。 */
export function rotateKey(target: KeyTarget = 'console', signal?: AbortSignal) {
  return api<ConsoleKeyView>('/api/system/console-key', {
    method: 'POST',
    body: JSON.stringify({ rotate: true, target }),
    signal,
  })
}

/** 只读读取完整密钥（POST reveal；GET 永远只回指纹）。 */
export function revealKey(target: KeyTarget = 'console', signal?: AbortSignal) {
  return api<ConsoleKeyView>('/api/system/console-key', {
    method: 'POST',
    body: JSON.stringify({ reveal: true, target }),
    signal,
  })
}

/** 轮换控制台密钥（既有调用保持原签名）。 */
export function rotateConsoleKey(signal?: AbortSignal) {
  return rotateKey('console', signal)
}

/** 轮换调用密钥（`{target:"proxy"}`）。 */
export function rotateProxyKey(signal?: AbortSignal) {
  return rotateKey('proxy', signal)
}
