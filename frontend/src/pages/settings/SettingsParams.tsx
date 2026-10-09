import { useEffect, useState } from 'react'
import { SlidersHorizontal } from '@phosphor-icons/react'
import { PageAlert } from '@/components/ui/PageAlert'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'
import { useSystemSettings } from '@/hooks/useSystemSettings'

import { ProxyCard } from './ProxyCard'
import { RoutingStrategyCard } from './RoutingStrategyCard'
import { SwitchSettingCard } from './SwitchSettingCard'

/**
 * 设置 · 运行参数页签（批次 4a 收编）：只保留会改变运行行为的参数——
 * 模型池 / 代理出口 / 停用签到 / 路由策略（签到窗口在设置 › 签到，批次 8 迁入）。
 */
export function SettingsParams() {
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
      <div className="space-y-4">
        <SkeletonBlock className="h-16 w-full rounded-2xl" />
        <div className="grid gap-4 xl:grid-cols-2">
          <SkeletonBlock className="h-40 w-full rounded-2xl" />
          <SkeletonBlock className="h-40 w-full rounded-2xl" />
          <SkeletonBlock className="h-32 w-full rounded-2xl" />
          <SkeletonBlock className="h-32 w-full rounded-2xl" />
        </div>
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

        <ProxyCard
          draft={proxyDraft}
          disabled={settingsBusy || !settings}
          onDraftChange={setProxyDraft}
          onCommit={(value) => void config.updateProxyURL(value)}
        />

        <RoutingStrategyCard
          settings={settings}
          busy={settingsBusy}
          onStrategy={(strategy) => void config.updateRoutingStrategy(strategy)}
        />
      </div>
    </div>
  )
}
