import { useEffect, useState } from 'react'
import { Gauge, SlidersHorizontal } from '@phosphor-icons/react'

import { RuntimeNoticesCard } from '@/components/system/RuntimeNoticesCard'
import { PageAlert } from '@/components/ui/PageAlert'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'
import { useSystemSettings } from '@/hooks/useSystemSettings'

import { ProxyCard } from './ProxyCard'
import { RoutingStrategyCard } from './RoutingStrategyCard'
import { SwitchSettingCard } from './SwitchSettingCard'

/**
 * 设置 · 通用页签：跨渠道的全局开关与策略——模型池 / 路由策略 / 倍率优选 /
 * 代理出口（全局兜底）/ 运行提醒（保活 + 告警 Webhook）。渠道级参数在「渠道」页。
 */
export function SettingsGeneral() {
  const { t } = useI18n()
  const [error, setError] = useState('')
  const config = useSystemSettings(setError)
  const { load } = config

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [load])

  if (config.loading && !config.settings) {
    return (
      <div className="grid gap-4 xl:grid-cols-2">
        <SkeletonBlock className="h-32 w-full rounded-2xl" />
        <SkeletonBlock className="h-40 w-full rounded-2xl" />
        <SkeletonBlock className="h-32 w-full rounded-2xl" />
        <SkeletonBlock className="h-40 w-full rounded-2xl" />
      </div>
    )
  }

  const { settings, proxyDraft, setProxyDraft, settingsBusy } = config

  return (
    <div className="space-y-5">
      {error ? <PageAlert title={error} /> : null}

      <div className="grid items-start gap-5 xl:grid-cols-2">
        <SwitchSettingCard
          icon={<SlidersHorizontal size={15} />}
          title={t('crossProviderModelPoolTitle')}
          hint={t('crossProviderModelPoolHint')}
          isSelected={settings?.cross_provider_model_pool ?? true}
          isDisabled={settingsBusy || !settings}
          ariaLabel={t('crossProviderModelPoolAriaLabel')}
          onChange={(selected) => void config.updateCrossProviderModelPool(selected)}
          statusLabel={t('crossProviderModelPoolStatus')}
          statusValue={settings?.cross_provider_model_pool ? t('enabled') : t('disabled')}
        />

        <SwitchSettingCard
          icon={<Gauge size={15} />}
          title={t('ratePreferenceTitle')}
          hint={t('ratePreferenceHint')}
          isSelected={settings?.rate_preference ?? false}
          isDisabled={settingsBusy || !settings}
          ariaLabel={t('ratePreferenceAriaLabel')}
          onChange={(selected) => void config.updateRatePreference(selected)}
          statusLabel={t('crossProviderModelPoolStatus')}
          statusValue={settings?.rate_preference ? t('enabled') : t('disabled')}
        />

        <RoutingStrategyCard
          settings={settings}
          busy={settingsBusy}
          onStrategy={(strategy) => void config.updateRoutingStrategy(strategy)}
        />

        <ProxyCard
          draft={proxyDraft}
          disabled={settingsBusy || !settings}
          onDraftChange={setProxyDraft}
          onCommit={(value) => void config.updateProxyURL(value)}
        />

        <RuntimeNoticesCard settings={settings} onSaved={config.setSettings} />
      </div>
    </div>
  )
}
