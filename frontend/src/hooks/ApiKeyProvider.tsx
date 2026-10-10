import { useMemo, useState, type ReactNode } from 'react'

import { API_KEY_STORAGE_KEY } from '@/lib/apiKeyStorage'

import { ApiKeyContext, type ApiKeyContextValue } from './ApiKeyContext'

export function ApiKeyProvider({ children }: { children: ReactNode }) {
  const [apiKey, setApiKeyState] = useState(() => localStorage.getItem(API_KEY_STORAGE_KEY) || '')

  const value = useMemo<ApiKeyContextValue>(() => ({
    apiKey,
    setApiKey: (next) => {
      const value = String(next || '').trim()
      if (value) localStorage.setItem(API_KEY_STORAGE_KEY, value)
      else localStorage.removeItem(API_KEY_STORAGE_KEY)
      setApiKeyState(value)
    },
    signOut: () => {
      localStorage.removeItem(API_KEY_STORAGE_KEY)
      setApiKeyState('')
    },
  }), [apiKey])

  return <ApiKeyContext.Provider value={value}>{children}</ApiKeyContext.Provider>
}
