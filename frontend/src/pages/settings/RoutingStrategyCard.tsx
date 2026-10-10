import { Description, Label, ListBox, Select } from '@heroui/react'
import { SlidersHorizontal } from '@phosphor-icons/react'
import type { SystemSettings } from '@/api/system'

import { FormRow } from '@/components/ui/FormRow'
import { SettingCard } from '@/components/ui/SettingCard'
import { useI18n } from '@/hooks/I18nContext'

type Props = {
  settings: SystemSettings | null
  busy: boolean
  onStrategy: (strategy: SystemSettings['routing_strategy']) => void
}

/** 路由策略卡：策略选择 + 会话亲和命中统计（只读）。 */
export function RoutingStrategyCard({ settings, busy, onStrategy }: Props) {
  const { t } = useI18n()
  return (
    <SettingCard icon={<SlidersHorizontal size={15} />} title={t('routingStrategyTitle')} hint={t('routingStrategyHint')}>
      <div className="border-t border-separator pt-4">
        <FormRow label={t('routingStrategy')}>
          <Select
            fullWidth
            aria-label={t('routingStrategy')}
            value={settings?.routing_strategy || 'round-robin'}
            isDisabled={busy || !settings}
            onChange={(value) => {
              if (typeof value === 'string' && value) onStrategy(value as SystemSettings['routing_strategy'])
            }}
          >
            <Select.Trigger className="items-center">
              <Select.Value className="min-w-0 truncate" />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox>
                <ListBox.Item id="round-robin" textValue="round-robin"><Label>{t('routingRoundRobin')}</Label><ListBox.ItemIndicator /></ListBox.Item>
                <ListBox.Item id="weighted-round-robin" textValue="weighted-round-robin"><Label>{t('routingWeightedRoundRobin')}</Label><ListBox.ItemIndicator /></ListBox.Item>
                <ListBox.Item id="fill-first" textValue="fill-first"><Label>{t('routingFillFirst')}</Label><ListBox.ItemIndicator /></ListBox.Item>
              </ListBox>
            </Select.Popover>
          </Select>
        </FormRow>
      </div>
      <div className="mt-4 grid grid-cols-2 gap-2 border-t border-separator pt-3 text-xs text-muted sm:grid-cols-4">
        <div>
          <span className="mono block text-sm font-medium text-foreground">
            {settings?.session_affinity?.ttl_seconds ? `${Math.round(settings.session_affinity.ttl_seconds / 60)}m` : '—'}
          </span>
          {t('sessionAffinityTTL')}
        </div>
        <div><span className="mono block text-sm font-medium text-foreground">{settings?.session_affinity?.hits ?? 0}</span>{t('sessionAffinityHits')}</div>
        <div><span className="mono block text-sm font-medium text-foreground">{settings?.session_affinity?.misses ?? 0}</span>{t('sessionAffinityMisses')}</div>
        <div><span className="mono block text-sm font-medium text-foreground">{settings?.session_affinity?.escapes ?? 0}</span>{t('sessionAffinityEscapes')}</div>
      </div>
      {settings?.session_affinity?.last_escape_reason ? <Description className="mt-3 text-xs">{t('lastSessionEscape')}: {settings.session_affinity.last_escape_reason}</Description> : null}
      {settings?.session_affinity?.last_miss_reason ? <Description className="mt-1 text-xs">{t('lastSessionMiss')}: {settings.session_affinity.last_miss_reason}</Description> : null}
    </SettingCard>
  )
}
