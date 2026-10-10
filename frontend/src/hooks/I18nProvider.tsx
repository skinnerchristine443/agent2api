import { useMemo, type ReactNode } from 'react'

import { translate } from '@/i18n/messages'

import { I18nContext, type I18nContextValue } from './I18nContext'

export function I18nProvider({ children }: { children: ReactNode }) {
  const value = useMemo<I18nContextValue>(() => ({
    t: (key, vars) => translate(key, vars),
  }), [])

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}
