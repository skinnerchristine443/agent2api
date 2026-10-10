import { useEffect, useState } from 'react'
import { Button, Description, Input, NumberField } from '@heroui/react'
import { fetchProviders, type ProviderDescriptor } from '@/api/overview'
import { updateSystemSettings, type AccountDefaults, type SystemSettings } from '@/api/system'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { FormRow } from '@/components/ui/FormRow'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'

/** 组合键 `provider.region`。 */
function comboKey(providerID: string, regionID: string) {
  return `${providerID}.${regionID}`
}

/**
 * 账号默认（渠道 × 区域）：最大并发 / 代理地址 / 丢弃系统提示词 / 保留额度下限 /
 * 每日 Token 上限 / 每日积分上限 / 单模型每日 Token 上限。
 *
 * 保存即 PATCH `account_defaults`，后端随后把该渠道所有账号物化为新值。
 * 优先级不在此列——它是账号级、在账号池列表内联编辑（设计决策 2026-10-10）。
 */
export function AccountDefaultsCard({ settings, onSaved }: { settings: SystemSettings | null; onSaved: (settings: SystemSettings) => void }) {
  const { t } = useI18n()
  const [providers, setProviders] = useState<ProviderDescriptor[] | null>(null)
  const [drafts, setDrafts] = useState<Record<string, AccountDefaults>>({})
  const [busyKey, setBusyKey] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    void fetchProviders().then((result) => {
      if (active) setProviders(result.data || [])
    }).catch((err) => { if (active) setError(String(err)) })
    return () => { active = false }
  }, [])

  function defaultsFor(providerID: string, regionID: string): AccountDefaults {
    const key = comboKey(providerID, regionID)
    if (drafts[key]) return drafts[key]
    const stored = settings?.account_defaults?.[key]
    if (stored) return stored
    // 后端未返回该组合时的兜底（与内置默认一致）。
    return {
      max_inflight: 4, proxy_url: '', drop_system_prompt: true,
      reserve_credits: 0, daily_token_limit: 0, daily_credit_limit: 0, daily_model_token_limit: 0,
    }
  }

  function change(providerID: string, regionID: string, patch: Partial<AccountDefaults>) {
    const key = comboKey(providerID, regionID)
    setDrafts((current) => ({ ...current, [key]: { ...defaultsFor(providerID, regionID), ...patch } }))
  }

  async function save(providerID: string, regionID: string) {
    const key = comboKey(providerID, regionID)
    const draft = defaultsFor(providerID, regionID)
    setBusyKey(key)
    setError('')
    try {
      onSaved(await updateSystemSettings({ account_defaults: { [key]: draft } }))
      setDrafts((current) => {
        const next = { ...current }
        delete next[key]
        return next
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusyKey('')
    }
  }

  if (error && !settings) return <PageAlert title={error} />

  return (
    <div className="space-y-5">
      {error ? <PageAlert title={error} /> : null}
      {!settings || !providers ? (
        <div className="grid gap-4 lg:grid-cols-2">
          <SkeletonBlock className="h-56 w-full rounded-2xl" />
          <SkeletonBlock className="h-56 w-full rounded-2xl" />
        </div>
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-2">
          {providers.flatMap((provider) => provider.regions.map((region) => {
            const key = comboKey(provider.id, region.id)
            const draft = defaultsFor(provider.id, region.id)
            const dirty = Boolean(drafts[key])
            return (
              <SectionCard key={key} title={`${provider.label} · ${region.label}`} hint={t('accountDefaultsHint')}>
                <div className="space-y-3">
                  <FormRow label={t('maxInflight')} hint={t('maxInflightHint')}>
                    <NumberField value={draft.max_inflight} onChange={(value) => change(provider.id, region.id, { max_inflight: value ?? 4 })} minValue={1} maxValue={32} aria-label={t('maxInflight')}>
                      <NumberField.Group>
                        <NumberField.DecrementButton />
                        <NumberField.Input />
                        <NumberField.IncrementButton />
                      </NumberField.Group>
                    </NumberField>
                  </FormRow>
                  <FormRow label={t('proxyUrl')} hint={t('proxyUrlHint')}>
                    <Input
                      value={draft.proxy_url || ''}
                      onChange={(event) => change(provider.id, region.id, { proxy_url: event.target.value })}
                      placeholder={t('proxyUrlPlaceholder')}
                      aria-label={t('proxyUrl')}
                    />
                  </FormRow>
                  <FormRow label={t('dropSystemPrompt')} hint={t('dropSystemPromptHint')}>
                    <CompactSwitch isSelected={draft.drop_system_prompt} ariaLabel={t('dropSystemPrompt')} onChange={(value) => change(provider.id, region.id, { drop_system_prompt: value })} />
                  </FormRow>
                  <FormRow label={t('dailyGuardReserve')} hint={t('dailyGuardReserveHint')}>
                    <NumberField value={draft.reserve_credits} onChange={(value) => change(provider.id, region.id, { reserve_credits: Math.max(0, Math.trunc(value ?? 0)) })} minValue={0} aria-label={t('dailyGuardReserve')}>
                      <NumberField.Group>
                        <NumberField.DecrementButton />
                        <NumberField.Input />
                        <NumberField.IncrementButton />
                      </NumberField.Group>
                    </NumberField>
                  </FormRow>
                  <FormRow label={t('dailyGuardTokenLimit')} hint={t('dailyGuardTokenLimitHint')}>
                    <NumberField value={draft.daily_token_limit} onChange={(value) => change(provider.id, region.id, { daily_token_limit: Math.max(0, Math.trunc(value ?? 0)) })} minValue={0} aria-label={t('dailyGuardTokenLimit')}>
                      <NumberField.Group>
                        <NumberField.DecrementButton />
                        <NumberField.Input />
                        <NumberField.IncrementButton />
                      </NumberField.Group>
                    </NumberField>
                  </FormRow>
                  <FormRow label={t('dailyGuardCreditLimit')} hint={t('dailyGuardCreditLimitHint')}>
                    <NumberField value={draft.daily_credit_limit} onChange={(value) => change(provider.id, region.id, { daily_credit_limit: Math.max(0, Math.trunc(value ?? 0)) })} minValue={0} aria-label={t('dailyGuardCreditLimit')}>
                      <NumberField.Group>
                        <NumberField.DecrementButton />
                        <NumberField.Input />
                        <NumberField.IncrementButton />
                      </NumberField.Group>
                    </NumberField>
                  </FormRow>
                  <FormRow label={t('dailyGuardModelTokenLimit')} hint={t('dailyGuardModelTokenLimitHint')}>
                    <NumberField value={draft.daily_model_token_limit} onChange={(value) => change(provider.id, region.id, { daily_model_token_limit: Math.max(0, Math.trunc(value ?? 0)) })} minValue={0} aria-label={t('dailyGuardModelTokenLimit')}>
                      <NumberField.Group>
                        <NumberField.DecrementButton />
                        <NumberField.Input />
                        <NumberField.IncrementButton />
                      </NumberField.Group>
                    </NumberField>
                  </FormRow>
                  <div className="flex items-center justify-between gap-3 pt-1">
                    <Description className="text-xs leading-5 text-muted">{t('accountDefaultsApplyHint')}</Description>
                    <Button size="sm" isDisabled={!dirty} isPending={busyKey === key} onPress={() => void save(provider.id, region.id)}>
                      {t('save')}
                    </Button>
                  </div>
                </div>
              </SectionCard>
            )
          }))}
        </div>
      )}
    </div>
  )
}
