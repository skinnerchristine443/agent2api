import { useEffect, useState } from 'react'

import { CheckinDefaults } from '@/components/system/CheckinDefaults'
import { PageAlert } from '@/components/ui/PageAlert'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useSystemSettings } from '@/hooks/useSystemSettings'

/**
 * 设置 · 签到页签（批次 8：自动签到时间段选择自福利 › 签到页迁入）。
 * 窗口编辑器复用 CheckinDefaults（按渠道能力列出 provider，逐渠道保存）。
 * 取数走 useSystemSettings（pages 层不直接 import `@/api`，见前端约定守卫）。
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
      <CheckinDefaults settings={settings} onSaved={config.setSettings} />
    </div>
  )
}
