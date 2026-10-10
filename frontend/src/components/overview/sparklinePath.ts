// Sparkline 的几何纯函数（与组件分文件：组件文件只导出组件，保持 Fast Refresh 有效）。

const round = (value: number) => Math.round(value * 100) / 100

/** 由数值序列构建折线与面积折线（导出便于单测）。 */
export function buildSparkPath(points: number[]): { line: string; area: string } | null {
  if (points.length < 2) return null
  const peak = Math.max(...points)
  const floor = Math.min(...points)
  const span = peak - floor || 1
  const stepX = 108 / (points.length - 1)
  const coords = points.map((value, index) => {
    const x = round(index * stepX)
    const y = round(30 - ((value - floor) / span) * 26)
    return `${x},${y}`
  })
  return {
    line: coords.join(' '),
    area: `0,34 ${coords.join(' ')} 108,34`,
  }
}
