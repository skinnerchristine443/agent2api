import type { AccountDefaults, SystemSettings } from '@/api/system'

/** 组合键 `provider.region`（对齐后端 `account_defaults` 的键）。 */
export function comboKey(providerID: string, regionID: string) {
  return `${providerID}.${regionID}`
}

/** 该组合的完整账号默认（含内置兜底）。
 *
 * ⚠️ 后端对 `account_defaults[key]` 是**整对象替换**（Normalize + Encode），
 * 不是字段合并 —— 任何保存都必须回填**完整 7 字段**，否则会把其余字段清零。 */
export function defaultsFor(settings: SystemSettings | null, providerID: string, regionID: string): AccountDefaults {
  const stored = settings?.account_defaults?.[comboKey(providerID, regionID)]
  if (stored) return stored
  return {
    max_inflight: 4,
    proxy_url: '',
    drop_system_prompt: true,
    reserve_credits: 0,
    daily_token_limit: 0,
    daily_credit_limit: 0,
    daily_model_token_limit: 0,
  }
}

/** 组合的代理地址（空 = 继承全局）。 */
export function proxyOf(settings: SystemSettings | null, providerID: string, regionID: string): string {
  return defaultsFor(settings, providerID, regionID).proxy_url || ''
}
