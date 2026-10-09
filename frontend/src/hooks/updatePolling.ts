export type UpdatePollInput = {
  /** 更新流程进行中（准备镜像 / 应用）。 */
  busy: boolean
  /** 刚完成更新（版本号已变化，等待自动刷新倒计时）。 */
  justUpdated: boolean
  /** 自动刷新倒计时秒数；非空即已进入收尾，不再轮询。 */
  reloadIn: number | null
}

/**
 * 更新状态轮询（2s/次）的启停条件：进行中或刚更新时轮询，完成即停。
 * 与改造前 `setInterval` 的守卫（`reloadIn != null || (!busy && !justUpdated)` 时提前返回）等价。
 */
export function updatePollActive({ busy, justUpdated, reloadIn }: UpdatePollInput): boolean {
  if (reloadIn != null) return false
  return busy || justUpdated
}
