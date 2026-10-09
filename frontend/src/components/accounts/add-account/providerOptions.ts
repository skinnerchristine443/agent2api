import { fetchProviders, type ProviderDescriptor } from '@/api/overview'
import type { DictKey, Translate } from '@/i18n/messages'

export type ProviderOption = {
  id: string
  provider: string
  region: string
  descriptor: ProviderDescriptor
}

// 用于没有专属 i18n 标签的 descriptor 的区域后缀。
const regionLabels: Record<string, string> = {
  global: 'Global',
  cn: 'CN',
}

// 为已知 family 提供专属 i18n 标签/提示；未知的 provider×region
// 组合回退到 descriptor，因此仅靠后端注册就能
// 把新类型加入此列表。
const labelKeys: Record<string, DictKey> = {
  'workbuddy-cn': 'accountTypeWorkBuddyCN',
  'workbuddy-global': 'accountTypeWorkBuddyGlobal',
  'trae-cn': 'accountTypeTraeCN',
}

const hintKeys: Record<string, DictKey> = {
  'workbuddy-cn': 'accountTypeWorkBuddyCNHint',
  'workbuddy-global': 'accountTypeWorkBuddyGlobalHint',
  'trae-cn': 'accountTypeTraeCNHint',
}

export async function loadProviderOptions(): Promise<ProviderOption[]> {
  const output = await fetchProviders().catch(() => null)
  const descriptors = output?.data || []
  const options: ProviderOption[] = []
  for (const descriptor of descriptors) {
    for (const region of descriptor.regions) {
      options.push({ id: `${descriptor.id}-${region.id}`, provider: descriptor.id, region: region.id, descriptor })
    }
  }
  return options
}

export function optionLabel(option: ProviderOption, t: Translate) {
  const key = labelKeys[option.id]
  if (key) {
    const localized = t(key)
    if (localized !== key) return localized
  }
  const regionLabel = option.descriptor.regions.find((region) => region.id === option.region)?.label || regionLabels[option.region] || ''
  const suffix = option.descriptor.regions.length > 1 && regionLabel ? ` ${regionLabel}` : ''
  return `${option.descriptor.label}${suffix}`.trim()
}

export function optionHint(option: ProviderOption | undefined, t: Translate) {
  if (!option) return ''
  const key = hintKeys[option.id]
  if (!key) return ''
  const localized = t(key)
  return localized === key ? '' : localized
}
