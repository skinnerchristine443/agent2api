import { ExpiryPage } from '@/pages/accounts/ExpiryPage'

/**
 * 额度页（批次 8：原福利 › 到期额度迁出为独立一级页，`/quota`）。
 * 组件复用 `ExpiryPage`（到期窗口 / 概览条 / 明细表 / `?account=` 直达不变）。
 */
export function QuotaPage() {
  return <ExpiryPage />
}
