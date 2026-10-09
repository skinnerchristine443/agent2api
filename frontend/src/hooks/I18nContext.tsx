import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { translate, type Translate } from '@/i18n/messages'

type I18nContextValue = {
  t: Translate
}

const I18nContext = createContext<I18nContextValue | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const value = useMemo<I18nContextValue>(() => ({
    t: (key, vars) => translate(key, vars),
  }), [])

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const ctx = useContext(I18nContext)
  if (!ctx) throw new Error('useI18n must be used within I18nProvider')
  return ctx
}
