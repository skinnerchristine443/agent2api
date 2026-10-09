import type { ProviderDescriptor } from '@/api/overview'
import type { AccountRow } from '@/lib/account'

/**
 * 账号 × 渠道能力判定（自 AccountsPage 内联函数提取，语义不变）：
 * provider 未实现某流程时不渲染对应控件（否则只是一个必然失败的按钮，
 * 网关会返回 provider_unsupported）。descriptor 省略 flag 视为可用。
 */

export function checkinPolicyFor(providers: ProviderDescriptor[], account: AccountRow) {
  return providers
    .find((provider) => provider.id === account.provider)
    ?.regions.find((region) => region.id === account.region)
    ?.checkin
}

export function authCapabilitiesFor(providers: ProviderDescriptor[], account: AccountRow) {
  return providers.find((provider) => provider.id === account.provider)?.capabilities
}

export function hasLoginAction(providers: ProviderDescriptor[], account: AccountRow) {
  const capabilities = authCapabilitiesFor(providers, account)
  return capabilities?.browser_login !== false || capabilities?.pat_login !== false
}
