import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

import { Segmented } from '@/components/ui/Segmented'
import { useI18n } from '@/hooks/I18nContext'
import type { DictKey } from '@/i18n/messages'

import { SettingsChannels } from './SettingsChannels'
import { SettingsCheckin } from './SettingsCheckin'
import { SettingsGeneral } from './SettingsGeneral'
import { SettingsKeys } from './SettingsKeys'
import { SettingsUpdate } from './SettingsUpdate'

export type SettingsTab = 'general' | 'channels' | 'checkin' | 'keys' | 'update'

const TABS: ReadonlyArray<{ id: SettingsTab; labelKey: DictKey }> = [
  { id: 'general', labelKey: 'settingsTabGeneral' },
  { id: 'channels', labelKey: 'settingsTabChannels' },
  { id: 'checkin', labelKey: 'settingsTabCheckin' },
  { id: 'keys', labelKey: 'settingsTabKeys' },
  { id: 'update', labelKey: 'settingsTabUpdate' },
]

/** 旧页签 id → 新 id（批次 16：运行参数并入「通用」）。 */
const LEGACY_TAB: Record<string, SettingsTab> = { params: 'general' }

function isSettingsTab(value: string | null): value is SettingsTab {
  return value === 'general' || value === 'channels' || value === 'checkin' || value === 'keys' || value === 'update'
}

/**
 * 设置页（批次 16 重组）：页内**五页签**——通用 / 渠道 / 签到 / 密钥 / 更新。
 * 页签状态入 URL（`?tab=`，默认 general 不写参）；旧路径经 NAV_REDIRECTS
 * 带 tab 直达；旧 `?tab=params` 兼容映射到「通用」。
 */
export function SettingsPage() {
  const { t } = useI18n()
  const [searchParams, setSearchParams] = useSearchParams()
  const raw = searchParams.get('tab')
  const tab: SettingsTab = isSettingsTab(raw) ? raw : (raw && LEGACY_TAB[raw]) || 'general'

  const setTab = useCallback((next: SettingsTab) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (next === 'general') params.delete('tab')
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

      {tab === 'general' ? <SettingsGeneral /> : null}
      {tab === 'channels' ? <SettingsChannels /> : null}
      {tab === 'checkin' ? <SettingsCheckin /> : null}
      {tab === 'keys' ? <SettingsKeys /> : null}
      {tab === 'update' ? <SettingsUpdate /> : null}
    </div>
  )
}
