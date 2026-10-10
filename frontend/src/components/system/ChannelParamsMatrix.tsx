import { useEffect, useMemo, useState } from 'react'
import { Button, Description, NumberField } from '@heroui/react'

import { fetchProviders, type ProviderDescriptor } from '@/api/overview'
import { updateSystemSettings, type AccountDefaults, type SystemSettings } from '@/api/system'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'

import { comboKey, defaultsFor } from '@/lib/channelDefaults'

type Props = {
  settings: SystemSettings | null
  onSaved: (settings: SystemSettings) => void
}

/** 参数行定义：标签 + 控件类型 + 值读写。 */
type RowDef = {
  key: keyof AccountDefaults
  labelKey: 'maxInflight' | 'dropSystemPrompt' | 'dailyGuardReserve' | 'dailyGuardTokenLimit' | 'dailyGuardCreditLimit' | 'dailyGuardModelTokenLimit'
  hintKey: 'maxInflightHint' | 'dropSystemPromptHint' | 'dailyGuardReserveHint' | 'dailyGuardTokenLimitHint' | 'dailyGuardCreditLimitHint' | 'dailyGuardModelTokenLimitHint'
  kind: 'number' | 'switch'
  min?: number
  max?: number
}

const ROWS: RowDef[] = [
  { key: 'max_inflight', labelKey: 'maxInflight', hintKey: 'maxInflightHint', kind: 'number', min: 1, max: 32 },
  { key: 'drop_system_prompt', labelKey: 'dropSystemPrompt', hintKey: 'dropSystemPromptHint', kind: 'switch' },
  { key: 'reserve_credits', labelKey: 'dailyGuardReserve', hintKey: 'dailyGuardReserveHint', kind: 'number', min: 0 },
  { key: 'daily_token_limit', labelKey: 'dailyGuardTokenLimit', hintKey: 'dailyGuardTokenLimitHint', kind: 'number', min: 0 },
  { key: 'daily_credit_limit', labelKey: 'dailyGuardCreditLimit', hintKey: 'dailyGuardCreditLimitHint', kind: 'number', min: 0 },
  { key: 'daily_model_token_limit', labelKey: 'dailyGuardModelTokenLimit', hintKey: 'dailyGuardModelTokenLimitHint', kind: 'number', min: 0 },
]

/**
 * 渠道运行参数矩阵：**行 = 参数，列 = 渠道（渠道 × 区域）**，一次保存应用到全部渠道。
 * 相较旧版「每渠道一张卡纵排」，本矩阵便于横向对比同一参数在不同渠道的取值。
 *
 * ⚠️ 后端 `account_defaults[key]` 是整对象替换 ⇒ 每格改动在完整对象上叠加。
 */
export function ChannelParamsMatrix({ settings, onSaved }: Props) {
  const { t } = useI18n()
  const [providers, setProviders] = useState<ProviderDescriptor[] | null>(null)
  const [drafts, setDrafts] = useState<Record<string, AccountDefaults>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [savedCount, setSavedCount] = useState<number | null>(null)

  useEffect(() => {
    let active = true
    void fetchProviders().then((result) => {
      if (active) setProviders(result.data || [])
    }).catch((err) => { if (active) setError(String(err)) })
    return () => { active = false }
  }, [])

  const combos = useMemo(
    () => (providers || []).flatMap((provider) => provider.regions.map((region) => ({
      providerID: provider.id,
      regionID: region.id,
      label: `${provider.label} · ${region.label}`,
      key: comboKey(provider.id, region.id),
    }))),
    [providers],
  )

  // 工作副本：以服务端值初始化；用户改动写进 drafts。
  function valueFor(key: string, providerID: string, regionID: string): AccountDefaults {
    return drafts[key] ?? defaultsFor(settings, providerID, regionID)
  }

  function change(providerID: string, regionID: string, patch: Partial<AccountDefaults>) {
    const key = comboKey(providerID, regionID)
    setSavedCount(null)
    setDrafts((current) => ({ ...current, [key]: { ...defaultsFor(settings, providerID, regionID), ...current[key], ...patch } }))
  }

  const dirty = combos.some((combo) => {
    const draft = drafts[combo.key]
    if (!draft) return false
    return JSON.stringify(draft) !== JSON.stringify(defaultsFor(settings, combo.providerID, combo.regionID))
  })

  async function save() {
    if (!providers) return
    setBusy(true)
    setError('')
    try {
      const payload: Record<string, AccountDefaults> = {}
      for (const combo of combos) payload[combo.key] = valueFor(combo.key, combo.providerID, combo.regionID)
      onSaved(await updateSystemSettings({ account_defaults: payload }))
      setDrafts({})
      setSavedCount(combos.length)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (!settings || !providers) return <SkeletonBlock className="h-64 w-full rounded-2xl" />

  return (
    <SectionCard
      title={t('channelMatrixTitle')}
      hint={t('channelMatrixHint')}
      right={(
        <div className="flex items-center gap-2">
          {savedCount != null && !dirty ? <span className="text-xs text-muted">{t('channelMatrixSaved', { n: savedCount })}</span> : null}
          <Button size="sm" isDisabled={!dirty} isPending={busy} onPress={() => void save()}>{t('channelMatrixSave')}</Button>
        </div>
      )}
    >
      {error ? <div className="mb-3"><PageAlert title={error} /></div> : null}
      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px] border-collapse text-sm">
          <thead>
            <tr className="border-b border-separator text-left text-xs text-muted">
              <th className="w-44 py-2 pr-4 font-medium">{t('routingStrategy')}</th>
              {combos.map((combo) => (
                <th key={combo.key} className="px-3 py-2 font-medium">{combo.label}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {ROWS.map((row) => (
              <tr key={row.key} className="border-b border-separator last:border-b-0">
                <td className="py-3 pr-4 align-middle">
                  <div className="text-xs font-medium text-foreground">{t(row.labelKey)}</div>
                  <Description className="mt-0.5 text-micro leading-4 text-muted">{t(row.hintKey)}</Description>
                </td>
                {combos.map((combo) => {
                  const value = valueFor(combo.key, combo.providerID, combo.regionID)
                  return (
                    <td key={combo.key} className="px-3 py-3 align-middle">
                      {row.kind === 'switch' ? (
                        <CompactSwitch
                          isSelected={Boolean(value[row.key])}
                          isDisabled={busy}
                          ariaLabel={`${combo.label} ${t(row.labelKey)}`}
                          onChange={(selected) => change(combo.providerID, combo.regionID, { [row.key]: selected })}
                        />
                      ) : (
                        <NumberField
                          value={Number(value[row.key]) || 0}
                          minValue={row.min ?? 0}
                          maxValue={row.max}
                          isDisabled={busy}
                          aria-label={`${combo.label} ${t(row.labelKey)}`}
                          onChange={(next) => change(combo.providerID, combo.regionID, { [row.key]: Math.max(row.min ?? 0, Math.trunc(next ?? 0)) })}
                        >
                          <NumberField.Group>
                            <NumberField.DecrementButton />
                            <NumberField.Input />
                            <NumberField.IncrementButton />
                          </NumberField.Group>
                        </NumberField>
                      )}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Description className="mt-3 text-micro text-muted">{t('accountDefaultsApplyHint')}</Description>
    </SectionCard>
  )
}
