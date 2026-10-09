/** 请求趋势图的统计窗口（API 口径为小时数；URL 口径为 `window` 片段）。 */
export type StatsWindow = 1 | 24 | 168

export const DEFAULT_STATS_WINDOW: StatsWindow = 24

const HOURS_BY_ID: Record<string, StatsWindow> = { '1h': 1, '24h': 24, '7d': 168 }
const ID_BY_HOURS: Record<StatsWindow, string> = { 1: '1h', 24: '24h', 168: '7d' }

/** 解析 `?window=`：缺省 / 未知取值回退默认窗口。 */
export function parseWindowParam(searchParams: URLSearchParams): StatsWindow {
  return HOURS_BY_ID[searchParams.get('window') ?? ''] ?? DEFAULT_STATS_WINDOW
}

/** 写回 `?window=`：默认窗口删除参数（保持链接干净），其余写入。 */
export function writeWindowParam(searchParams: URLSearchParams, hours: StatsWindow): URLSearchParams {
  const next = new URLSearchParams(searchParams)
  if (hours === DEFAULT_STATS_WINDOW) {
    next.delete('window')
    return next
  }
  next.set('window', ID_BY_HOURS[hours])
  return next
}
