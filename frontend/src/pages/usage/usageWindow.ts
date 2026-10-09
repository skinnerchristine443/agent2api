import type { DictKey } from '@/i18n/messages'

// 统计窗口（URL 参数 `window`）↔ API `days` 的映射（方案 §7.3）：
//   取值域 1d / 7d / 30d，分别映射 days=1 / 7 / 30；默认 7d 且不写入 URL。
// 说明：映射刻意留在页面目录内（未并入 lib/url.ts），避免与并行批次的
// url 工具改动产生合并冲突；如后续收口到 lib，逐项搬迁即可。

export const USAGE_WINDOWS = ['1d', '7d', '30d'] as const

export type UsageWindow = (typeof USAGE_WINDOWS)[number]

export const DEFAULT_USAGE_WINDOW: UsageWindow = '7d'

const WINDOW_DAYS: Record<UsageWindow, number> = { '1d': 1, '7d': 7, '30d': 30 }

const WINDOW_LABEL_KEYS = {
  '1d': 'usageWindow1d',
  '7d': 'usageWindow7d',
  '30d': 'usageWindow30d',
} as const satisfies Record<UsageWindow, DictKey>

/** URL 取值 → 窗口；非法 / 缺失一律回退默认（默认值不写入 URL）。 */
export function parseUsageWindow(raw: string | null): UsageWindow {
  return USAGE_WINDOWS.includes(raw as UsageWindow) ? raw as UsageWindow : DEFAULT_USAGE_WINDOW
}

/** 窗口 → API `days`。 */
export function usageWindowDays(window: UsageWindow) {
  return WINDOW_DAYS[window]
}

/** 窗口 → 按钮文案 key。 */
export function usageWindowLabelKey(window: UsageWindow) {
  return WINDOW_LABEL_KEYS[window]
}
