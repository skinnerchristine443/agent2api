import type { ModelInfo } from '@/api/types'
import { reasoningLevelKey, type Translate } from '@/i18n/messages'

// 模型目录的元数据辅助（纯函数）：渠道 / 区域 / 设置键 / 行键 / 路由名 / 推理档位文案。
// 与筛选逻辑（modelsFilter.ts）分离，便于各自单测与复用。

/** PATCH / 去重的设置键（settings_key 优先）。 */
export function modelSettingsKey(model: ModelInfo) {
  return model.settings_key || model.id
}

export function modelProvider(model: ModelInfo) {
  // 账号 family 优先于上游 owned_by（后者可能是模型供应商）。
  return String(model.provider || model.owned_by || '').trim().toLowerCase()
}

export function modelRegion(model: ModelInfo) {
  const region = String(model.region || '').trim().toLowerCase()
  if (region) return region
  const regions = model.regions || []
  return String(regions[0] || '').trim().toLowerCase()
}

export function modelRowKey(model: ModelInfo) {
  const native = model.native_model || model.mapped_key || ''
  return `${modelProvider(model)}:${modelRegion(model) || 'any'}:${modelSettingsKey(model)}:${native}`
}

/** 路由名（上游映射名与展示名不同才有意义，否则返回空串）。 */
export function routedModelName(model: ModelInfo) {
  const routeName = model.route_display_name || ''
  return routeName && routeName !== (model.display_name || model.id) ? routeName : ''
}

/** 推理档位 code → 本地化文案（未知 code 原样展示）。 */
export function reasoningLabel(t: Translate, level: string) {
  const key = reasoningLevelKey(level)
  return key ? t(key) : level
}

/** 模型清单行渲染（表格列 / 移动端卡片）共用的事件与状态。 */
export type ModelListHandlers = {
  savingKey: string
  onDetails: (model: ModelInfo) => void
  onToggleMaxMode: (model: ModelInfo, selected: boolean) => void
  onReasoningChange: (model: ModelInfo, next: string) => void
}

export type ModelListProps = ModelListHandlers & {
  models: ModelInfo[]
}

