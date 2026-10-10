import { useEffect, useState } from 'react'

import { AccountDefaultsCard } from '@/components/system/AccountDefaultsCard'
import { RuntimeNoticesCard } from '@/components/system/RuntimeNoticesCard'
import { CheckinDefaults } from '@/components/system/CheckinDefaults'
import { PageAlert } from '@/components/ui/PageAlert'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useSystemSettings } from '@/hooks/useSystemSettings'

/**
 * 设置 · 签到页签：自动签到时间段（按渠道）+ 账号默认（渠道 × 区域）。
 * 窗口编辑器复用 CheckinDefaults；账号默认复用 AccountDefaultsCard。
 */
export function SettingsCheckin() {
  const [error, setError] = useState('')
  const config = useSystemSettings(setError)
  const { load, settings } = config

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [load])

  if (config.loading && !settings) return <SkeletonBlock className="h-40 w-full rounded-2xl" />

  return (
    <div className="space-y-5">
      {error ? <PageAlert title={error} /> : null}
      <RuntimeNoticesCard settings={settings} onSaved={config.setSettings} />
      <AccountDefaultsCard settings={settings} onSaved={config.setSettings} />
      <CheckinDefaults settings={settings} onSaved={config.setSettings} />
    </div>
  )
}
