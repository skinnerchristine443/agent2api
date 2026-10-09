// 用量表格 / 指标卡的展示格式化（纯函数）。
// formatSpeed 渲染聚合解码速率：没有采样时显示短横线，而不是伪造的零。

export function formatSpeed(value: number) {
  return value > 0 ? value.toFixed(1) : '—'
}

export function formatPercent(value: number) {
  return value > 0 ? `${(value * 100).toFixed(0)}%` : '—'
}

/** 分组键文案：后端哨兵值 `(unknown)` / `(unassigned)` → 本地化标签。 */
export function groupLabel(
  key: string,
  labels: { unknown: string; unassigned: string },
) {
  if (key === '(unknown)') return labels.unknown
  if (key === '(unassigned)') return labels.unassigned
  return key
}
