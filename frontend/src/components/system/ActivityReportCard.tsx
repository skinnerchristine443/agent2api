import { useState } from 'react'
import { Input } from '@heroui/react'
import { updateSystemSettings, type SystemSettings } from '@/api/system'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { FormRow } from '@/components/ui/FormRow'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { useI18n } from '@/hooks/I18nContext'

/**
 * 对话活跃上报（点亮 growth 连登）：开关 + 本地时刻。保存即 PATCH。
 *
 * 默认关闭。开启后每号每天在配置时刻向上游发一条 `chat_request_send` 事件
 * （通道 `POST {billingBase}/v2/report`），点亮连登天数；上报成功后会回读
 * 连登天数自检（发现「200 但静默丢弃」）。仅对 WorkBuddy 渠道生效。
 */
export function ActivityReportCard({ settings, onSaved }: { settings: SystemSettings | null; onSaved: (settings: SystemSettings) => void }) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function save(input: { activity_report_enabled?: boolean; activity_report_time?: string }) {
    setBusy(true)
    setError('')
    try {
      onSaved(await updateSystemSettings(input))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (!settings) return null

  return (
    <SectionCard title={t('activityReportTitle')} hint={t('activityReportHint')}>
      {error ? <PageAlert title={error} /> : null}
      <div className="space-y-3">
        <FormRow label={t('activityReportEnabled')} hint={t('activityReportEnabledHint')}>
          <CompactSwitch
            isSelected={settings.activity_report_enabled ?? false}
            isDisabled={busy}
            ariaLabel={t('activityReportEnabled')}
            onChange={(value) => void save({ activity_report_enabled: value })}
          />
        </FormRow>
        <FormRow label={t('activityReportTime')} hint={t('activityReportTimeHint')}>
          <Input
            type="time"
            value={settings.activity_report_time || '09:00'}
            disabled={busy || !settings.activity_report_enabled}
            aria-label={t('activityReportTime')}
            onChange={(event) => void save({ activity_report_time: event.target.value })}
          />
        </FormRow>
      </div>
    </SectionCard>
  )
}
