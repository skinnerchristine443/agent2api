import { useMemo } from 'react'

import { buildSparkPath } from './sparklinePath'

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
