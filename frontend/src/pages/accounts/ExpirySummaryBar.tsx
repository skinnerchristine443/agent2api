import { Banner } from '@/components/ui/Banner'
import { SectionCard } from '@/components/ui/SectionCard'
import type { Translate } from '@/i18n/messages'
import { formatQuotaAmount } from '@/lib/account'
import { accountChannelLabelByKey } from '@/lib/provider'

import { summarizeExpiryByChannel, type ExpiryRow, type UnitTotal } from './expiryModel'

function totalsText(totals: UnitTotal[]) {
  if (!totals.length) return ''
  return totals
    .map((total) => `${formatQuotaAmount(total.amount)}${total.unit ? ` ${total.unit}` : ''}`)
    .join(' · ')
}

/** 单组数据格：计数 + 该组剩余合计（桌面两行；窄屏 label 同行内联）。 */
function GroupCell({ label, count, totals, tone }: {
  label: string
  count: number
  totals?: UnitTotal[]
  tone?: string
}) {
  const amounts = totals ? totalsText(totals) : ''
  const countClass = ['mono text-sm font-semibold', count > 0 && tone ? tone : ''].filter(Boolean).join(' ')
  return (
    <div className="flex min-w-0 items-center justify-between gap-2 sm:block">
      <span className="shrink-0 text-xs text-muted sm:hidden">{label}</span>
      <div className="flex min-w-0 flex-col items-end sm:items-start">
        <span className={countClass}>{count}</span>
        {amounts ? <span className="max-w-full truncate text-xs text-muted" title={amounts}>{amounts}</span> : null}
      </div>
    </div>
  )
}

/**
 * 到期总计栏（批次 12：按渠道**竖向 3 行**；13 与左栏**等高**；14 删「更远」列）——
 * WorkBuddy / Trae CN 各一行，行内三组列（主窗口内 / 次窗口内 / 未上报；
 * 「更远」不再入栏，明细表仍保留该组）显示计数与剩余合计；桌面为「表头 + 3 行」
 * 表格式，窄屏（<640）折每渠道网格。顶部保留到期排序关闭态横幅。
 */
export function ExpirySummaryBar({ rows, sortDisabled, t }: {
  rows: ExpiryRow[]
  sortDisabled: boolean
  t: Translate
}) {
  const channels = summarizeExpiryByChannel(rows)

  return (
    <div className="flex h-full flex-col gap-3">
      {sortDisabled ? (
        <Banner
          status="warning"
          title={t('expirySortDisabled')}
          description={t('expirySortDisabledHint')}
        />
      ) : null}
      <SectionCard className="flex-1" title={t('expirySummaryTitle')} hint={t('expirySummaryHint')}>
        {/* 表头（≥640px）：渠道 ｜ 主窗口内 ｜ 次窗口内 ｜ 未上报 */}
        <div className="hidden gap-x-4 pb-1.5 text-xs text-muted sm:grid sm:grid-cols-[132px_repeat(3,minmax(0,1fr))]">
          <span>{t('expiryChannelColumn')}</span>
          <span>{t('expiryGroupPrimary')}</span>
          <span>{t('expiryGroupSecondary')}</span>
          <span>{t('expiryGroupUnreported')}</span>
        </div>
        <div className="divide-y divide-separator">
          {channels.map(({ channel, accountCount, summary }) => (
            <div
              key={channel}
              className="grid grid-cols-2 items-center gap-x-4 gap-y-2 py-2.5 sm:grid-cols-[132px_repeat(3,minmax(0,1fr))] sm:items-start"
            >
              <div className="col-span-2 min-w-0 sm:col-span-1">
                <div className="truncate text-sm font-semibold text-foreground">{accountChannelLabelByKey(channel)}</div>
                <div className="text-xs text-muted">{t('expiryChannelAccounts', { n: accountCount })}</div>
              </div>
              <GroupCell label={t('expiryGroupPrimary')} count={summary.primary.count} totals={summary.primary.totals} tone="text-warning" />
              <GroupCell label={t('expiryGroupSecondary')} count={summary.secondary.count} totals={summary.secondary.totals} />
              <GroupCell label={t('expiryGroupUnreported')} count={summary.unreported.count} />
            </div>
          ))}
        </div>
      </SectionCard>
    </div>
  )
}
