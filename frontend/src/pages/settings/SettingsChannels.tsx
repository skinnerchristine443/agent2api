import { useEffect, useState } from 'react'

import { ChannelParamsMatrix } from '@/components/system/ChannelParamsMatrix'
import { ProxyGridCard } from '@/components/system/ProxyGridCard'
import { PageAlert } from '@/components/ui/PageAlert'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useSystemSettings } from '@/hooks/useSystemSettings'

/**
 * 设置 · 渠道页签：按「渠道 × 区域」集中配置——① 代理地址（独立栏目，4 框）
 * ② 运行参数矩阵（行 = 参数，列 = 渠道，整表一次保存）。两者同源（account_defaults）。
 */
export function SettingsChannels() {
  const [error, setError] = useState('')
  const config = useSystemSettings(setError)
  const { load, settings } = config

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [load])

  if (config.loading && !settings) return <SkeletonBlock className="h-64 w-full rounded-2xl" />

  return (
    <div className="space-y-5">
      {error ? <PageAlert title={error} /> : null}
      <ProxyGridCard settings={settings} onSaved={config.setSettings} />
      <ChannelParamsMatrix settings={settings} onSaved={config.setSettings} />
    </div>
  )
}
