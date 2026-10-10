// 模型详情的展示格式化（与组件分文件：组件文件只导出组件，保持 Fast Refresh 有效）。

/** Token 数量的紧凑展示：1.5M / 128k / 原始数字；空值短横线。 */
export function formatTokens(value?: number) {
  if (!value) return '—'
  if (value >= 1_000_000) return `${(value / 1_000_000).toString().replace(/\.0$/, '')}M`
  if (value >= 1000) return `${Math.round(value / 1000)}k`
  return String(value)
}
