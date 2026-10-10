import { createContext, useContext } from 'react'

export type ApiKeyContextValue = {
  apiKey: string
  setApiKey: (value: string) => void
  signOut: () => void
}

// Context 与 Provider 分文件：Provider 文件只导出组件（保持 Fast Refresh 有效）。
export const ApiKeyContext = createContext<ApiKeyContextValue | null>(null)

export function useApiKey() {
  const ctx = useContext(ApiKeyContext)
  if (!ctx) throw new Error('useApiKey must be used within ApiKeyProvider')
  return ctx
}
