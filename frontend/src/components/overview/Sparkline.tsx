import { useMemo } from 'react'

type Props = {
  /** 数值序列（左 → 右）。不足 2 点不渲染。 */
  points: number[]
  className?: string
}

/**
 * 迷你趋势线（原型 metric-card 的 spark）：面积 + 折线，108×34 viewBox
 * 拉伸填充。y 轴按 min–max 归一（贴合波形形态），上缘 4 / 下缘 30 留边。
 */
export function Sparkline({ points, className }: Props) {
  const path = useMemo(() => buildSparkPath(points), [points])
  if (!path) return null
  return (
    <svg className={className} viewBox="0 0 108 34" preserveAspectRatio="none" aria-hidden="true">
      <polyline className="spark-area" points={path.area} />
      <polyline className="spark-line" points={path.line} />
    </svg>
  )
}

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
