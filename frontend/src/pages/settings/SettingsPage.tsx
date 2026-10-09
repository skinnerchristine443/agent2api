import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

import { Segmented } from '@/components/ui/Segmented'
import { useI18n } from '@/hooks/I18nContext'
import type { DictKey } from '@/i18n/messages'

import { SettingsCheckin } from './SettingsCheckin'
import { SettingsKeys } from './SettingsKeys'
import { SettingsParams } from './SettingsParams'
import { SettingsUpdate } from './SettingsUpdate'

export type SettingsTab = 'params' | 'checkin' | 'keys' | 'update'

const TABS: ReadonlyArray<{ id: SettingsTab; labelKey: DictKey }> = [
  { id: 'params', labelKey: 'settingsTabParams' },
  { id: 'checkin', labelKey: 'settingsTabCheckin' },
  { id: 'keys', labelKey: 'settingsTabKeys' },
  { id: 'update', labelKey: 'settingsTabUpdate' },
]

function isSettingsTab(value: string | null): value is SettingsTab {
  return value === 'params' || value === 'checkin' || value === 'keys' || value === 'update'
}

/**
 * 设置页（设计 D14；批次 8 加签到页签）：页内四页签——运行参数 / 签到 / 密钥 / 更新。
 * 页签状态入 URL（`?tab=`，默认 params 不写参）；旧路径经 NAV_REDIRECTS
 * 带 tab 直达（/system/keys → /settings?tab=keys、/system/update → /settings?tab=update）。
 */
export function SettingsPage() {
  const { t } = useI18n()
  const [searchParams, setSearchParams] = useSearchParams()
  const raw = searchParams.get('tab')
  const tab: SettingsTab = isSettingsTab(raw) ? raw : 'params'

  const setTab = useCallback((next: SettingsTab) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (next === 'params') params.delete('tab')
      else params.set('tab', next)
      return params
    }, { replace: true })
  }, [setSearchParams])

  return (
    <div className="space-y-5">
      <div data-gsap-reveal>
        <Segmented
          ariaLabel={t('navSettings')}
          value={tab}
          onChange={setTab}
          items={TABS.map((item) => ({ id: item.id, label: t(item.labelKey) }))}
        />
      </div>

      {tab === 'params' ? <SettingsParams /> : null}
      {tab === 'checkin' ? <SettingsCheckin /> : null}
      {tab === 'keys' ? <SettingsKeys /> : null}
      {tab === 'update' ? <SettingsUpdate /> : null}
    </div>
  )
}
