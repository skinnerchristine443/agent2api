import { useCallback, useState } from 'react'
import { fetchSystemSettings, updateSystemSettings, type SystemSettings } from '@/api/system'

/**
 * 系统设置读写（原 SystemPage 的设置区逻辑原样搬移）：模型池 / 停用签到 /
 * 代理 URL / 路由策略，均为「乐观更新 + 失败回滚 + 共享 error」语义。
 */
export function useSystemSettings(setError: (message: string) => void) {
  const [settings, setSettings] = useState<SystemSettings | null>(null)
  const [proxyDraft, setProxyDraft] = useState('')
  const [settingsBusy, setSettingsBusy] = useState(false)
  /** 首次读取是否在途（页面骨架门；成功 / 失败都结束）。 */
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    try {
      const result = await fetchSystemSettings()
      setSettings(result)
      setProxyDraft(result.proxy_url || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [setError])

  async function updateCrossProviderModelPool(enabled: boolean) {
    const previous = settings?.cross_provider_model_pool ?? true
    setSettings((current) => current ? { ...current, cross_provider_model_pool: enabled } : current)
    setSettingsBusy(true)
    setError('')
    try {
      setSettings(await updateSystemSettings({ cross_provider_model_pool: enabled }))
    } catch (err) {
      setSettings((current) => current ? { ...current, cross_provider_model_pool: previous } : current)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSettingsBusy(false)
    }
  }

  async function updateProxyURL(value: string) {
    const saved = settings?.proxy_url || ''
    // 字段没有任何变化：保持草稿原样并跳过 PATCH，
    // 使无效的失焦绝不会重新加载 worker。
    if (value === saved) {
      setProxyDraft(saved)
      return
    }
    const previousDraft = proxyDraft
    setProxyDraft(value)
    setSettings((current) => current ? { ...current, proxy_url: value } : current)
    setSettingsBusy(true)
    setError('')
    try {
      const updated = await updateSystemSettings({ proxy_url: value })
      setSettings(updated)
      setProxyDraft(updated.proxy_url || '')
    } catch (err) {
      setProxyDraft(previousDraft)
      setSettings((current) => current ? { ...current, proxy_url: saved } : current)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSettingsBusy(false)
    }
  }

  async function updateRoutingStrategy(strategy: SystemSettings['routing_strategy']) {
    const previous = settings?.routing_strategy || 'round-robin'
    setSettings((current) => current ? { ...current, routing_strategy: strategy } : current)
    setSettingsBusy(true)
    setError('')
    try {
      setSettings(await updateSystemSettings({ routing_strategy: strategy }))
    } catch (err) {
      setSettings((current) => current ? { ...current, routing_strategy: previous } : current)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSettingsBusy(false)
    }
  }

  return {
    settings,
    setSettings,
    proxyDraft,
    setProxyDraft,
    settingsBusy,
    loading,
    load,
    updateCrossProviderModelPool,
    updateProxyURL,
    updateRoutingStrategy,
  }
}
