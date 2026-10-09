/**
 * 401 登出的暂缓窗口 —— 密钥轮换的「竞态抑制」（方案 §4.4 ⑫ P0）。
 *
 * 轮换在途期间（`holdSignOut` 未释放）以及新钥写回后的短暂宽限期内，401 收口
 * 不得调用 `signOut`：旧钥的**在途**请求可能迟到返回 401，照常登出会清掉刚
 * 写回的新钥——而 GET 只回指纹，新钥一旦丢失在界面内再也取不回来。
 *
 * 只抑制「登出」这一个副作用；错误本身仍按各调用方原语义展示。
 */
let holdCount = 0
let graceUntil = 0

/** 写回新钥后仍可能有旧钥请求在途，故释放后再宽限一小段时间。 */
export const SIGN_OUT_GRACE_MS = 2000

/** 持有一次抑制，返回释放函数（幂等，可安全重复调用）。 */
export function holdSignOut(): () => void {
  holdCount += 1
  graceUntil = 0
  let released = false
  return () => {
    if (released) return
    released = true
    holdCount = Math.max(0, holdCount - 1)
    if (holdCount === 0) graceUntil = Date.now() + SIGN_OUT_GRACE_MS
  }
}

/** 当前是否应暂缓 401 登出（轮换在途 / 写回后的宽限期内）。 */
export function signOutSuspended(): boolean {
  return holdCount > 0 || Date.now() < graceUntil
}
