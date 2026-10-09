import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'

// 单元测试默认运行在 node 环境（纯函数）；组件 / hooks 测试用文件头部的
// `@vitest-environment happy-dom` 注释切换到 DOM 环境（vitest 5 已移除
// environmentMatchGlobs）。仅保留 `@` 别名，与 vite.config.ts 保持同步。
// setupFiles 负责组件测试的 DOM 清理（node 环境下自动跳过）。
export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
    // 时区固定：控制台按「本地时区」渲染时间（如 inboxEvents 的 HH:mm），
    // 若测试跟随宿主 TZ 漂移，会在 CI（UTC）与开发机（+08:00）之间出现纯环境性
    // 断言失败（2026-10-09 CI 首跑实证：'00:20' vs '08:20'）。这里把整套单测的
    // TZ 钉在 Asia/Shanghai，与开发者机器的实际显示口径一致，使断言在任何调度
    // 环境都可复现。
    env: { TZ: 'Asia/Shanghai' },
    // 隔离性必须在本机与 CI 两种调度下都成立：收口批的共享 worker 实验
    // （isolate: false）曾于本机两遍无 flake，但 CI 首跑即失败——受限
    // worker 调度下（本机 --maxWorkers=2 可复现）跨文件共享模块注册表
    // 与模块级状态（useApiQuery 在途表 / 同一 api 模块的多文件 mock），
    // 症状为 mock 零调用与断言超时。按审查 P3-19 预案回退为默认隔离。
    setupFiles: ['src/test/setup.ts'],
  },
})
