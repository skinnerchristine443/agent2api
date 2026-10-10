import { useState } from 'react'
import { Input } from '@heroui/react'
import { updateSystemSettings, type SystemSettings } from '@/api/system'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { FormRow } from '@/components/ui/FormRow'
import { PageAlert } from '@/components/ui/PageAlert'
import { SectionCard } from '@/components/ui/SectionCard'
import { useI18n } from '@/hooks/I18nContext'

/**
 * 运行提醒：保活（开关 + 本地时刻）+ 告警 Webhook 出口。
 *
 * 保活 = 每日定时向上游发一次轻量请求维持会话；Webhook = 把额度/账号告警
 * 推到可配 URL（默认关闭）。保存即 PATCH。
 */
export function RuntimeNoticesCard({ settings, onSaved }: { settings: SystemSettings | null; onSaved: (settings: SystemSettings) => void }) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [webhookDraft, setWebhookDraft] = useState<string | null>(null)

  async function save(input: { keepalive_enabled?: boolean; keepalive_time?: string; webhook_url?: string }) {
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
  const webhookValue = webhookDraft ?? settings.webhook_url ?? ''

  return (
    <SectionCard title={t('runtimeNoticesTitle')} hint={t('runtimeNoticesHint')}>
      {error ? <PageAlert title={error} /> : null}
      <div className="space-y-3">
        <FormRow label={t('keepaliveEnabled')} hint={t('keepaliveEnabledHint')}>
          <CompactSwitch
            isSelected={settings.keepalive_enabled ?? true}
            isDisabled={busy}
            ariaLabel={t('keepaliveEnabled')}
            onChange={(value) => void save({ keepalive_enabled: value })}
          />
        </FormRow>
        <FormRow label={t('keepaliveTime')} hint={t('keepaliveTimeHint')}>
          <Input
            type="time"
            value={settings.keepalive_time || '22:00'}
            disabled={busy}
            aria-label={t('keepaliveTime')}
            onChange={(event) => void save({ keepalive_time: event.target.value })}
          />
        </FormRow>
        <FormRow label={t('webhookUrl')} hint={t('webhookUrlHint')}>
          <div className="flex items-center gap-2">
            <Input
              value={webhookValue}
              disabled={busy}
              placeholder="https://example.com/hook"
              aria-label={t('webhookUrl')}
              onChange={(event) => setWebhookDraft(event.target.value)}
            />
            <button
              type="button"
              className="text-xs text-muted hover:text-foreground"
              disabled={busy || webhookDraft === null}
              onClick={() => void save({ webhook_url: webhookValue }).then(() => setWebhookDraft(null))}
            >
              {t('save')}
            </button>
          </div>
        </FormRow>
      </div>
    </SectionCard>
  )
}
