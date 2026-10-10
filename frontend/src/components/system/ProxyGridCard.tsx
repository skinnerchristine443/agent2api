import { useEffect, useState } from 'react'
import { Input } from '@heroui/react'

import { fetchProviders, type ProviderDescriptor } from '@/api/overview'
import { updateSystemSettings, type SystemSettings } from '@/api/system'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'

import { comboKey, defaultsFor, proxyOf } from '@/lib/channelDefaults'

type Props = {
  settings: SystemSettings | null
  onSaved: (settings: SystemSettings) => void
}

/**
 * 代理地址（按渠道）：每个「渠道 × 区域」一个输入框，失焦即存（未变化跳过 PATCH）。
 * 数据 = `account_defaults[渠道].proxy_url`（与渠道参数矩阵同源，此处单独抽出集中填写）。
 *
 * ⚠️ 后端对 `account_defaults[key]` 是整对象替换 ⇒ 保存时用 `defaultsFor` 回填完整 7 字段。
 */
export function ProxyGridCard({ settings, onSaved }: Props) {
  const { t } = useI18n()
  const [providers, setProviders] = useState<ProviderDescriptor[] | null>(null)
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [busyKey, setBusyKey] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    void fetchProviders().then((result) => {
      if (active) setProviders(result.data || [])
    }).catch((err) => { if (active) setError(String(err)) })
    return () => { active = false }
  }, [])

  async function commit(providerID: string, regionID: string, value: string) {
    const key = comboKey(providerID, regionID)
    if (value === proxyOf(settings, providerID, regionID)) {
      setDrafts((current) => { const next = { ...current }; delete next[key]; return next })
      return
    }
    setBusyKey(key)
    setError('')
    try {
      // 整对象替换：以该组合当前完整默认为基础，只改 proxy_url。
      const merged = { ...defaultsFor(settings, providerID, regionID), proxy_url: value }
      onSaved(await updateSystemSettings({ account_defaults: { [key]: merged } }))
      setDrafts((current) => { const next = { ...current }; delete next[key]; return next })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusyKey('')
    }
  }

  if (!settings || !providers) return <SkeletonBlock className="h-40 w-full rounded-2xl" />

  return (
    <SectionCard title={t('proxyGridTitle')} hint={t('proxyGridHint')}>
      {error ? <div className="mb-3"><PageAlert title={error} /></div> : null}
      <div className="grid gap-4 sm:grid-cols-2">
        {providers.flatMap((provider) => provider.regions.map((region) => {
          const key = comboKey(provider.id, region.id)
          const value = drafts[key] ?? proxyOf(settings, provider.id, region.id)
          return (
            <div key={key} className="space-y-1.5">
              <label className="text-xs font-medium text-muted">{provider.label} · {region.label}</label>
              <Input
                value={value}
                placeholder={t('proxyUrlPlaceholder')}
                disabled={busyKey === key}
                onChange={(event) => setDrafts((current) => ({ ...current, [key]: event.target.value }))}
                onBlur={(event) => void commit(provider.id, region.id, event.target.value.trim())}
                aria-label={`${provider.label} ${region.label} ${t('proxyUrl')}`}
              />
            </div>
          )
        }))}
      </div>
    </SectionCard>
  )
}
