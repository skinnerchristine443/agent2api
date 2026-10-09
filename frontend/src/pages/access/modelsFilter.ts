import type { ProviderDescriptor } from '@/api/overview'
import type { ModelInfo } from '@/api/types'
import type { Translate } from '@/i18n/messages'
import { accountProviderLabel } from '@/lib/provider'

import { modelProvider, modelRegion } from './modelMeta'

// 模型目录的筛选状态（URL 字段登记，方案 §7.3）：
//   q        —— 搜索关键词（模型名 / 上游 key / 渠道 / 倍率）
//   provider —— 渠道筛选，取值 `<provider>:<region>`（如 trae:cn），空串 = 全部
// 默认值不写入 URL；解析 / 过滤全在本模块（纯函数，带单测）。

export type ModelsFilters = {
  q: string
  provider: string
}

/** 筛选稳定键：变化即触发 usePagedQuery 回落第 1 页（§7.3「筛选变化重置分页」）。 */
export function modelsFiltersKey(filters: ModelsFilters) {
  return `${filters.q.trim()}\u0000${filters.provider.trim()}`
}

export type ProviderRegionOption = {
  value: string
  provider: string
  region: string
}

/** 渠道描述符 → 「渠道:区域」筛选选项（无区域的渠道不产生选项）。 */
export function providerRegionOptions(descriptors: ProviderDescriptor[]): ProviderRegionOption[] {
  const options: ProviderRegionOption[] = []
  for (const descriptor of descriptors) {
    for (const region of descriptor.regions || []) {
      if (!region?.id) continue
      options.push({ value: `${descriptor.id}:${region.id}`, provider: descriptor.id, region: region.id })
    }
  }
  return options
}

/** `provider:region` → { provider, region }；无冒号时整体视作渠道。 */
export function splitProviderFilter(value: string) {
  const trimmed = value.trim()
  if (!trimmed.includes(':')) return { provider: trimmed, region: '' }
  const [provider, region] = trimmed.split(':', 2)
  return { provider, region }
}

/** 应用 `q` + `provider` 筛选（与迁移前 ProvidersPage 的过滤口径一致）。 */
export function filterModels(models: ModelInfo[], filters: ModelsFilters, t: Translate) {
  const query = filters.q.trim().toLowerCase()
  const { provider: filterProvider, region: filterRegion } = splitProviderFilter(filters.provider)
  return models.filter((model) => {
    const provider = modelProvider(model)
    const region = modelRegion(model)
    if (filterProvider && provider !== filterProvider) return false
    if (filterRegion && region !== filterRegion) return false
    if (!query) return true
    const providerLabel = accountProviderLabel(provider, region || undefined, t)
    const haystack = [
      model.display_name || '',
      model.id,
      model.mapped_key || '',
      provider,
      providerLabel,
      model.owned_by || '',
      region,
      model.credits || '',
      model.free ? 'free' : '',
    ]
    return haystack.join(' ').toLowerCase().includes(query)
  })
}
