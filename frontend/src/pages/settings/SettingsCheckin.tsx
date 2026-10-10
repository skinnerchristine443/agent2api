import { useEffect, useState } from 'react'

import { ActivityReportCard } from '@/components/system/ActivityReportCard'
import { CheckinDefaults } from '@/components/system/CheckinDefaults'
import { PageAlert } from '@/components/ui/PageAlert'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useSystemSettings } from '@/hooks/useSystemSettings'

/**
 * 设置 · 签到页签：自动签到时间段（按渠道）+「停用账号也自动签到」开关
 * + 对话活跃上报（点亮连登，与签到同属每日定时任务）。
 * （批次 16：运行提醒迁「通用」页、账号默认迁「渠道」页，本页只留签到本身。）
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
      <ActivityReportCard settings={settings} onSaved={config.setSettings} />
    </div>
  )
}
