import { createContext, useContext } from 'react'

import type { Translate } from '@/i18n/messages'

export type I18nContextValue = {
  t: Translate
}

// Context 与 Provider 分文件：Provider 文件只导出组件（保持 Fast Refresh 有效）。
export const I18nContext = createContext<I18nContextValue | null>(null)

export function useI18n() {
  const ctx = useContext(I18nContext)
  if (!ctx) throw new Error('useI18n must be used within I18nProvider')
  return ctx
}
