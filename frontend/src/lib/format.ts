export function formatCompact(value: number) {
  if (!Number.isFinite(value)) return '—'
  const abs = Math.abs(value)
  if (abs >= 1_000_000) return `${trimFixed(value / 1_000_000)}M`
  if (abs >= 1000) return `${trimFixed(value / 1000)}k`
  return String(Math.round(value))
}

// 概览域（RankBoard / StatsCards）的延迟展示：整秒去尾零（`1s` / `1.5s`）。
// 与 `logsFormat.formatLatency` 有意不同——后者面向日志表格，保留一位小数
// （`1.0s`）以对齐等宽列宽。两者非等价，勿合并。
export function formatLatency(ms: number | null | undefined) {
  if (ms == null || !Number.isFinite(ms)) return '—'
  if (ms >= 1000) return `${trimFixed(ms / 1000)}s`
  return `${Math.round(ms)}ms`
}

export function formatBytes(bytes: number | null | undefined) {
  if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${trimFixed(value)} ${units[unit]}`
}

// 概览域（CountUp）的百分比：小值留一位小数（`5.0%`）。
// 与 `pages/usage/usageFormat.formatPercent` 有意不同——后者面向用量表，
// 整数百分比、0 显示为「—」。两者非等价，勿合并。
export function formatPercent(rate: number | null | undefined) {
  if (rate == null || !Number.isFinite(rate)) return '—'
  const pct = rate * 100
  return `${pct >= 10 ? pct.toFixed(0) : pct.toFixed(1)}%`
}

export function formatCountKind(value: number, kind: 'int' | 'compact' | 'percent' | 'ms') {
  if (kind === 'compact') return formatCompact(value)
  if (kind === 'percent') return formatPercent(value)
  if (kind === 'ms') return formatLatency(value)
  return String(Math.round(value))
}

/** 官方 WorkBuddy catalog 的 credits 文案（存在时）。 */
export function modelCreditsText(model: { credits?: string | null }) {
  const credits = (model.credits || '').trim()
  return credits || ''
}

export function modelIsFree(model: { free?: boolean | null; credits?: string | null }) {
  if (model.free) return true
  const credits = modelCreditsText(model).toLowerCase()
  if (!credits) return false
  const match = credits.match(/(\d+(?:\.\d+)?)/)
  return Boolean(match && Number(match[1]) === 0)
}

function trimFixed(value: number) {
  const digits = Math.abs(value) >= 10 ? 0 : 1
  return Number(value.toFixed(digits)).toString()
}
