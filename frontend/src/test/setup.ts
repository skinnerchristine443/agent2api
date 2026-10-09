import { afterEach } from 'vitest'

// 组件测试的 DOM 清理：@testing-library/react 的自动 cleanup 依赖全局
// afterEach（本项目未开启 vitest globals），因此在此显式注册。
// node 环境的纯函数测试没有 document，直接跳过（不加载 RTL）。
afterEach(async () => {
  if (typeof document === 'undefined') return
  const { cleanup } = await import('@testing-library/react')
  cleanup()
})
