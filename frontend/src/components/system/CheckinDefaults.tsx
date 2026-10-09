import { useEffect, useState } from 'react'
import { Description, Input } from '@heroui/react'
import { fetchProviders, type ProviderDescriptor } from '@/api/overview'
import { updateSystemSettings, type CheckinWindow, type SystemSettings } from '@/api/system'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'

const clockPattern = /^([01]\d|2[0-3]):[0-5]\d$/

// 每日奖励仅在固定本地时间开放的 provider 不得早于该时间开启主窗口；
// 与后端策略保持一致（当前没有任何渠道声明此类下限）。
const minMainStart: Record<string, string> = {}

// 当某签到 provider 尚无已存窗口（或它的设置读取失败）时
// 渲染此值，使运维人员仍能获得可编辑的输入。
const emptyWindow: CheckinWindow = { main_start: '', main_end: '', fallback_start: '', fallback_end: '' }

type WindowProblem = 'format' | 'order' | 'fallback' | 'minimum'

function windowProblem(providerID: string, window: CheckinWindow): WindowProblem | null {
  const fields = [window.main_start, window.main_end, window.fallback_start, window.fallback_end]
  if (fields.some((value) => !clockPattern.test(value))) return 'format'
  if (window.main_start >= window.main_end || window.fallback_start >= window.fallback_end) return 'order'
  if (window.fallback_start < window.main_end) return 'fallback'
  const min = minMainStart[providerID]
  if (min && window.main_start < min) return 'minimum'
  return null
}

/**
 * 自动签到（批次 9 版式）：**2×2 四卡**——WorkBuddy / Trae 两个渠道窗口卡 +
 * 「停用账号也自动签到」开关卡（自运行参数页迁入）。每卡独立校验、保存即 PATCH。
 */
export function CheckinDefaults({ settings, onSaved }: { settings: SystemSettings | null; onSaved: (settings: SystemSettings) => void }) {
  const { t } = useI18n()
  const [providers, setProviders] = useState<ProviderDescriptor[] | null>(null)
  const [drafts, setDrafts] = useState<Record<string, CheckinWindow>>({})
  const [problems, setProblems] = useState<Record<string, WindowProblem>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    void fetchProviders().then((result) => {
      if (active) setProviders((result.data || []).filter((provider) => provider.regions.some((region) => region.checkin)))
    }).catch((err) => { if (active) setError(String(err)) })
    return () => { active = false }
  }, [])

  function windowFor(providerID: string): CheckinWindow {
    return drafts[providerID] ?? settings?.checkin_windows?.[providerID] ?? emptyWindow
  }

  function problemText(problem: WindowProblem): string {
    if (problem === 'fallback') return t('checkinWindowFallbackHint')
    if (problem === 'minimum') return t('checkinWindowMinimumHint')
    return t('checkinWindowInvalid')
  }

  async function save(providerID: string, next: CheckinWindow) {
    const problem = windowProblem(providerID, next)
    setProblems((current) => {
      const updated = { ...current }
      if (problem) updated[providerID] = problem
      else delete updated[providerID]
      return updated
    })
    if (problem || !settings) return
    if (JSON.stringify(next) === JSON.stringify(settings.checkin_windows?.[providerID])) return
    setBusy(true)
    setError('')
    try {
      onSaved(await updateSystemSettings({ checkin_windows: { [providerID]: next } }))
      setDrafts((current) => {
        const updated = { ...current }
        delete updated[providerID]
        return updated
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  function change(providerID: string, field: keyof CheckinWindow, value: string) {
    const next = { ...windowFor(providerID), [field]: value }
    setDrafts((current) => ({ ...current, [providerID]: next }))
    void save(providerID, next)
  }

  async function saveDisabledAccounts(next: boolean) {
    if (!settings) return
    setBusy(true)
    setError('')
    try {
      onSaved(await updateSystemSettings({ checkin_disabled_accounts: next }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  function windowFields(provider: ProviderDescriptor, window: CheckinWindow) {
    return (
      <>
        <div className="space-y-1">
          <Description className="text-xs leading-5 text-muted">{t('checkinMainWindow')}</Description>
          <div className="flex items-center gap-2">
            <Input
              type="time"
              value={window.main_start}
              disabled={busy}
              aria-label={`${provider.label} ${t('checkinMainWindow')} ${t('checkinWindowStart')}`}
              onChange={(event) => change(provider.id, 'main_start', event.target.value)}
            />
            <span className="text-muted">–</span>
            <Input
              type="time"
              value={window.main_end}
              disabled={busy}
              aria-label={`${provider.label} ${t('checkinMainWindow')} ${t('checkinWindowEnd')}`}
              onChange={(event) => change(provider.id, 'main_end', event.target.value)}
            />
          </div>
        </div>
        <div className="space-y-1">
          <Description className="text-xs leading-5 text-muted">{t('checkinFallbackWindow')}</Description>
          <div className="flex items-center gap-2">
            <Input
              type="time"
              value={window.fallback_start}
              disabled={busy}
              aria-label={`${provider.label} ${t('checkinFallbackWindow')} ${t('checkinWindowStart')}`}
              onChange={(event) => change(provider.id, 'fallback_start', event.target.value)}
            />
            <span className="text-muted">–</span>
            <Input
              type="time"
              value={window.fallback_end}
              disabled={busy}
              aria-label={`${provider.label} ${t('checkinFallbackWindow')} ${t('checkinWindowEnd')}`}
              onChange={(event) => change(provider.id, 'fallback_end', event.target.value)}
            />
          </div>
        </div>
      </>
    )
  }

  if (error && !settings) {
    return <PageAlert title={error} />
  }

  return (
    <div className="space-y-5">
      {error ? <PageAlert title={error} /> : null}
      {!settings || !providers ? (
        <div className="grid gap-4 lg:grid-cols-2">
          <SkeletonBlock className="h-40 w-full rounded-2xl" />
          <SkeletonBlock className="h-40 w-full rounded-2xl" />
          <SkeletonBlock className="h-40 w-full rounded-2xl" />
          <SkeletonBlock className="h-40 w-full rounded-2xl" />
        </div>
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-2">
          {providers.map((provider) => {
            const window = windowFor(provider.id)
            const problem = problems[provider.id]
            const hint = provider.regions
              .filter((region) => region.checkin)
              .map((region) => `${region.label} · ${region.checkin?.timezone === 'Local' ? settings.timezone : region.checkin?.timezone}`)
              .join(' / ')
            return (
              <SectionCard key={provider.id} title={provider.label} hint={hint}>
                <div className="space-y-3">
                  {windowFields(provider, window)}
                  {problem ? <Description className="text-xs leading-5 text-danger">{problemText(problem)}</Description> : null}
                </div>
              </SectionCard>
            )
          })}

          {/* 停用账号也自动签到（批次 9 自运行参数页迁入；与渠道卡同网格 2×2）。 */}
          <SectionCard title={t('checkinDisabledAccountsTitle')} hint={t('checkinDisabledAccountsHint')}>
            <div className="flex items-center justify-between gap-3">
              <span className="text-xs text-muted">
                {t('checkinDisabledAccountsStatus')}：{settings.checkin_disabled_accounts ? t('enabled') : t('disabled')}
              </span>
              <CompactSwitch
                isSelected={settings.checkin_disabled_accounts ?? false}
                isDisabled={busy}
                ariaLabel={t('checkinDisabledAccountsAriaLabel')}
                onChange={(selected) => void saveDisabledAccounts(selected)}
              />
            </div>
          </SectionCard>
        </div>
      )}
    </div>
  )
}
