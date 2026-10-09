import { useState } from 'react'
import { Button, Chip } from '@heroui/react'
import { Check, Copy, Key } from '@phosphor-icons/react'
import { useNavigate } from 'react-router-dom'

import { KeyValue } from '@/components/ui/KeyValue'
import { SectionCard } from '@/components/ui/SectionCard'
import { StatusDot } from '@/components/ui/StatusDot'
import { useI18n } from '@/hooks/I18nContext'
import { copyText } from '@/lib/clipboard'

import { maskApiKey } from './accessText'

/** 连接区：Base URL（可复制）+ 协议/认证口径 + 调用密钥状态（轮换入口指向「密钥管理」）。 */
export function ConnectionCard({
  base,
  apiKey,
  ready,
}: {
  base: string
  apiKey: string
  ready: boolean
}) {
  const { t } = useI18n()
  const navigate = useNavigate()
  const [copied, setCopied] = useState(false)

  async function copyBase() {
    await copyText(base)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1100)
  }

  const maskedKey = maskApiKey(apiKey)

  return (
    <SectionCard
      title={t('connection')}
      right={<Chip size="sm" variant="soft" color={ready ? 'success' : 'warning'}>{ready ? t('endpointReady') : t('degraded')}</Chip>}
    >
      <div className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="text-xs font-medium text-muted">{t('baseUrl')}</div>
            <code className="mono mt-0.5 block truncate text-sm font-medium text-foreground">{base}</code>
          </div>
          <Button size="sm" variant="secondary" onPress={() => void copyBase()}>
            {copied ? <Check size={14} /> : <Copy size={14} />}
            {t('copyBaseUrl')}
          </Button>
        </div>

        <KeyValue
          items={[
            { label: t('protocol'), value: 'HTTP / SSE' },
            { label: t('authentication'), value: 'Bearer API key' },
            {
              label: t('accessKeyCurrent'),
              value: maskedKey ? <code className="mono text-xs">{maskedKey}</code> : t('missing'),
            },
          ]}
        />

        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-separator pt-3">
          <div className="flex min-w-0 items-start gap-2">
            <StatusDot state={apiKey ? 'ok' : 'danger'} className="mt-1.5" />
            <div className="min-w-0 text-xs">
              <div className="font-medium text-foreground">{t('accessKeyStatus')}</div>
              <p className="mt-0.5 leading-5 text-muted">{t('accessKeyStatusHint')}</p>
            </div>
          </div>
          <Button size="sm" variant="ghost" onPress={() => navigate('/settings?tab=keys')}>
            <Key size={14} />
            {t('accessKeyManage')}
          </Button>
        </div>
      </div>
    </SectionCard>
  )
}
