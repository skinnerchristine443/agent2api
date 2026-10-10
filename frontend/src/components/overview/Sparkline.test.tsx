import { describe, expect, it } from 'vitest'

import { buildSparkPath } from './sparklinePath'

describe('Sparkline 路径构建', () => {
  it('按 min–max 归一映射到 108×34 视区（上缘 4 / 下缘 30）', () => {
    const path = buildSparkPath([0, 10, 5])
    expect(path).not.toBeNull()
    // 3 点均分 x：0 / 54 / 108
    expect(path!.line).toBe('0,30 54,4 108,17')
    // 面积折线自左下角闭合
    expect(path!.area).toBe('0,34 0,30 54,4 108,17 108,34')
  })

  it('全等值不除零（span 兜底 1，输出平线）', () => {
    const path = buildSparkPath([7, 7, 7])
    expect(path!.line).toBe('0,30 54,30 108,30')
  })

  it('不足 2 点返回 null', () => {
    expect(buildSparkPath([])).toBeNull()
    expect(buildSparkPath([5])).toBeNull()
  })
})
