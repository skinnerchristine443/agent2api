import type { Translate } from '@/i18n/messages'

export function isWorkBuddyProvider(provider?: string) {
  return String(provider || '').toLowerCase() === 'workbuddy'
}

export function isTraeProvider(provider?: string) {
  return String(provider || '').toLowerCase() === 'trae'
}

/**
 * 渠道键（批次 15：渠道 × 区域五组合）——`<provider>-<region>`（region 缺省时退化为
 * provider 本体）；与添加向导的 providerOptions.id 同形（`workbuddy-cn` 等）。
 */
export function accountChannelKey(provider?: string, region?: string) {
  const providerID = String(provider || '').toLowerCase()
  const regionID = String(region || '').toLowerCase()
  return regionID ? `${providerID}-${regionID}` : providerID
}

/** 渠道键 → 展示名（WorkBuddy CN / Trae CN …；未知键回退原名）。 */
export function accountChannelLabelByKey(key: string) {
  const raw = String(key || '').toLowerCase()
  const dash = raw.indexOf('-')
  if (dash === -1) return accountChannelLabel(raw)
  return accountChannelLabel(raw.slice(0, dash), raw.slice(dash + 1))
}

/** 渠道 × 区域的简洁展示名（批次 15：五组合页签 / 额度栏 / 概览渠道榜共用）。 */
export function accountChannelLabel(provider?: string, region?: string) {
  const providerID = String(provider || '').toLowerCase()
  const regionID = String(region || '').toLowerCase()
  const base = isWorkBuddyProvider(providerID) ? 'WorkBuddy' : isTraeProvider(providerID) ? 'Trae' : String(provider || '')
  if (!regionID) return base
  return `${base} ${regionID === 'global' ? 'Global' : regionID.toUpperCase()}`
}

export function accountProviderLabel(
  provider: string | undefined,
  region: string | undefined,
  t: Translate,
) {
  const providerID = String(provider || '').toLowerCase()
  const regionID = String(region || '').toLowerCase()
  if (isWorkBuddyProvider(providerID)) {
    return regionID === 'global' ? t('accountTypeWorkBuddyGlobal') : t('accountTypeWorkBuddyCN')
  }
  if (isTraeProvider(providerID)) {
    return t('accountTypeTraeCN')
  }
  return provider || t('account')
}

// tieredTraeCaps 解析 Trae max-mode 开关所对应、用于展示的字段：
// 上下文窗口，以及对声明了 Max 档位的模型而言，还有 prompt 与 output 上限。
// 它读取不可变的 catalog 默认值加上 *_max 档位，并按 maxMode 选择，
// 因此从不依赖（也不修改）上一次渲染的值：关闭开关总会重新得到默认档位。
export type TraeTierCaps = {
  context_length?: number
  prompt_max_tokens?: number
  max_output_tokens?: number
}

export function tieredTraeCaps(
  model: {
    catalog_context_length?: number
    default_context_length?: number
    context_length?: number
    catalog_context_length_max?: number
    max_mode?: boolean
    prompt_max_tokens?: number
    prompt_max_tokens_max?: number
    max_output_tokens?: number
    max_output_tokens_max?: number
  },
  maxMode?: boolean,
): TraeTierCaps {
  const on = maxMode ?? Boolean(model.max_mode)
  const dev = model.catalog_context_length || model.default_context_length || model.context_length || 0
  const max = model.catalog_context_length_max || 0
  if (on && max > 0) {
    return {
      context_length: max,
      prompt_max_tokens: model.prompt_max_tokens_max || model.prompt_max_tokens,
      max_output_tokens: model.max_output_tokens_max || model.max_output_tokens,
    }
  }
  return {
    context_length: dev,
    prompt_max_tokens: model.prompt_max_tokens,
    max_output_tokens: model.max_output_tokens,
  }
}
