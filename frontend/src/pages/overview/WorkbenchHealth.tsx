import { Link } from 'react-router-dom'

import type { SystemSettings } from '@/api/system'
import type { Overview } from '@/api/types'
import { StatusDot } from '@/components/ui/StatusDot'
import { useI18n } from '@/hooks/I18nContext'
import { quickCategory, type AccountRow } from '@/lib/account'

/**
 * 工作台顶部健康条（设计 D16 / 密度治理 §3.1）：把原「运行状态条」与
 * 「账号池健康概览」合并为一条——左半是服务健康（代理 / 调度 / 出口 /
 * 时区 / 运行时长），右半是账号池四计数（点任一计数跳对应快捷视图，
 * 与账号池摘要条同一份口径 `quickCategory`，单一事实源）。
 */
export function WorkbenchHealth({ overview, accounts, settings }: {
  overview: Overview | null
  accounts: AccountRow[]
  settings: SystemSettings | null
}) {
  const { t } = useI18n()
  const proxyOk = Boolean(overview?.proxy?.ok)
  const workerOk = Boolean(overview?.worker?.ok)

  const counts = { total: accounts.length, avail: 0, attn: 0, transit: 0 }
  for (const account of accounts) {
    const category = quickCategory(account)
    if (category === 'avail') counts.avail += 1
    else if (category === 'attn') counts.attn += 1
    else if (category === 'transit') counts.transit += 1
  }

  const exit = sanitizeProxy(settings?.proxy_url, t('healthDirect'))
  const uptime = uptimeLabel(overview?.uptime_seconds)
  const countsView = [
    { key: 'total', label: t('accountCount'), value: counts.total, tone: undefined, to: '/accounts' },
    { key: 'avail', label: t('availableAccounts'), value: counts.avail, tone: 'text-success-fg', to: '/accounts?quick=avail' },
    { key: 'attn', label: t('needsAttention'), value: counts.attn, tone: 'text-danger-fg', to: '/accounts?quick=attn' },
    { key: 'transit', label: t('hbInTransit'), value: counts.transit, tone: 'text-warning-fg', to: '/accounts?quick=transit' },
  ]

  return (
    <section
      data-gsap-reveal
      className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-2xl border border-border bg-surface px-4 py-2.5"
    >
      <HealthItem label={t('healthProxy')} ok={proxyOk}>{proxyOk ? t('running') : t('degraded')}</HealthItem>
      <Sep />
      <HealthItem label={t('healthWorker')} ok={workerOk}>{workerOk ? t('running') : t('degraded')}</HealthItem>
      <Sep />
      <span className="inline-flex items-center gap-1.5 text-sm text-muted">
        {t('healthExit')}
        <span className="mono font-medium text-foreground">{exit}</span>
      </span>
      <Sep />
      <span className="inline-flex items-center gap-1.5 text-sm text-muted">
        {t('healthTimezone')}
        <span className="font-medium text-foreground">{settings?.timezone || '—'}</span>
      </span>
      <Sep />
      <span className="inline-flex items-center gap-1.5 text-sm text-muted">
        {t('healthUptime')}
        <span className="mono font-medium text-foreground">{uptime}</span>
      </span>

      <span className="min-w-4 flex-1" />

      <div className="flex flex-wrap items-center gap-0.5">
        {countsView.map((item) => (
          <Link
            key={item.key}
            to={item.to}
            title={t('healthAccountsJump')}
            className="inline-flex items-center gap-1.5 rounded-lg px-2 py-1 transition-colors hover:bg-surface-hover"
          >
            <span className={`mono text-sm font-semibold ${item.tone || 'text-foreground'}`}>{item.value}</span>
            <span className="text-xs text-muted">{item.label}</span>
          </Link>
        ))}
      </div>
    </section>
  )
}

function HealthItem({ label, ok, children }: { label: string; ok: boolean; children: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-muted">
      <StatusDot state={ok ? 'ok' : 'danger'} />
      {label}
      <span className="font-medium text-foreground">{children}</span>
    </span>
  )
}

function Sep() {
  return <span aria-hidden="true" className="h-[18px] w-px bg-border" />
}

/** 出口展示（脱敏：仅 host，去掉协议与凭据；空配置显示 direct）。 */
function sanitizeProxy(url: string | undefined, fallback: string) {
  const raw = (url || '').trim()
  if (!raw) return fallback
  try {
    const parsed = new URL(raw)
    return parsed.host || fallback
  } catch {
    return raw.replace(/^[a-z0-9+.-]+:\/\//i, '').replace(/^[^@/]*@/, '')
  }
}

/** 运行时长（`6d 04h` / `3h 12m` / `8m`；无数据时 `—`）。 */
function uptimeLabel(seconds: number | undefined) {
  if (!seconds || seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}d ${String(hours).padStart(2, '0')}h`
  if (hours > 0) return `${hours}h ${String(minutes).padStart(2, '0')}m`
  return `${minutes}m`
}
