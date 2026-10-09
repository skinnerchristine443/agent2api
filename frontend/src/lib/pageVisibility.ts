// 页面可见性抽象：`document.visibilityState` / `visibilitychange` 的唯一直连点。
// 轮询类 hook 一律经这里判断与订阅，不直接触碰 document；同时它也解决了测试
// 环境的限制——happy-dom 的 visibilityState 是只读 getter，且不会自动派发
// visibilitychange，单测只能用 defineProperty + 手动 dispatchEvent 模拟，
// 因此把这一层收口到本模块后，测试与生产走同一条实现路径。

export function isPageVisible() {
  if (typeof document === 'undefined') return true
  return document.visibilityState !== 'hidden'
}

/** 订阅可见性变化；返回退订函数。listener 收到的是「当前是否可见」。 */
export function subscribePageVisibility(listener: (visible: boolean) => void) {
  if (typeof document === 'undefined') return () => {}
  const handler = () => listener(isPageVisible())
  document.addEventListener('visibilitychange', handler)
  return () => document.removeEventListener('visibilitychange', handler)
}
