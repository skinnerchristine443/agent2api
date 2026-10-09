import { Fragment, useState } from 'react'
import { Button } from '@heroui/react'
import { ArrowClockwise, CaretDown, CaretRight } from '@phosphor-icons/react'

import type { AccountQuotaPackage } from '@/api/types'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import type { Translate } from '@/i18n/messages'
import { formatQuotaAmount } from '@/lib/account'
import { accountProviderLabel } from '@/lib/provider'

import {
  EXPIRY_GROUP_ORDER,
  absoluteExpiryLabel,
  expiryGroupLabelKey,
  relativeExpiryLabel,
  type ExpiryGroupKey,
  type ExpiryRow,
} from './expiryModel'

type Props = {
  groups: Record<ExpiryGroupKey, ExpiryRow[]>
  collapsed: Partial<Record<ExpiryGroupKey, boolean>>
  onToggleGroup: (group: ExpiryGroupKey) => void
  highlightId: string
  refreshingId: string
  onRefresh: (id: string) => void
  now: number
  t: Translate
}

function packageEndLabel(pkg: AccountQuotaPackage) {
  if (pkg.end_time) return pkg.end_time
  return pkg.ends_at ? absoluteExpiryLabel(pkg.ends_at) : ''
}

function PackageRows({ packages, t }: { packages: AccountQuotaPackage[]; t: Translate }) {
  if (!packages.length) {
    return <div className="px-3 py-2 text-xs text-muted">{t('expiryNoPackages')}</div>
  }
  return (
    <table className="w-full border-collapse text-xs">
      <thead>
        <tr className="text-micro text-muted">
          <th className="px-3 py-1.5 text-left font-medium">{t('expiryColPackageSize')}</th>
          <th className="px-3 py-1.5 text-right font-medium">{t('expiryColPackageRemain')}</th>
          <th className="px-3 py-1.5 text-right font-medium">{t('expiryColPackageEnd')}</th>
        </tr>
      </thead>
      <tbody>
        {packages.map((pkg, index) => (
          <tr key={`${pkg.ends_at || 0}-${index}`} className="border-t border-separator/60">
            <td className="mono px-3 py-1.5">{formatQuotaAmount(pkg.size)}{pkg.unit ? ` ${pkg.unit}` : ''}</td>
            <td className="mono px-3 py-1.5 text-right">{formatQuotaAmount(pkg.remain)}</td>
            <td className="mono px-3 py-1.5 text-right text-muted">{packageEndLabel(pkg) || '—'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function ExpiryRowCells({ row, open, highlight, refreshing, onToggle, onRefresh, now, t }: {
  row: ExpiryRow
  open: boolean
  highlight: boolean
  refreshing: boolean
  onToggle: () => void
  onRefresh: () => void
  now: number
  t: Translate
}) {
  const unreported = row.group === 'unreported'
  return (
    <tr className={['border-t border-separator align-top', highlight ? 'bg-warning/5' : ''].filter(Boolean).join(' ')}>
      <td className="max-w-[260px] px-3 py-2.5">
        <div className="flex items-start gap-2">
          <button
            type="button"
            onClick={onToggle}
            aria-expanded={open}
            aria-label={t('expiryTogglePackages')}
            className="mt-0.5 grid size-5 shrink-0 place-items-center rounded text-muted transition-colors hover:bg-surface-secondary hover:text-foreground"
          >
            {open ? <CaretDown size={12} /> : <CaretRight size={12} />}
          </button>
          <div className="min-w-0">
            <div className="truncate text-sm font-medium" title={row.name}>{row.name}</div>
            <div className="mono truncate text-micro text-muted" title={row.id}>{row.id}</div>
          </div>
        </div>
      </td>
      <td className="px-3 py-2.5 text-xs text-muted">{accountProviderLabel(row.provider, row.region, t)}</td>
      <td className="px-3 py-2.5 text-xs">
        {unreported ? (
          <div>
            <div className="text-muted">{t('expiryGroupUnreported')}</div>
            <div className="text-micro text-muted">{t('expiryUnreportedReason')}</div>
          </div>
        ) : (
          <div>
            <div className="font-medium">{relativeExpiryLabel(row.expiresAt, now, t)}</div>
            <div className="mono text-micro text-muted">{absoluteExpiryLabel(row.expiresAt)}</div>
          </div>
        )}
      </td>
      <td className="mono px-3 py-2.5 text-right text-xs">
        {unreported || row.expiringRemain <= 0
          ? '—'
          : `${formatQuotaAmount(row.expiringRemain)}${row.unit ? ` ${row.unit}` : ''}`}
      </td>
      <td className="mono px-3 py-2.5 text-right text-xs text-muted">{row.packages.length || '—'}</td>
      <td className="px-3 py-2.5 text-right">
        <Button
          isIconOnly
          size="sm"
          variant="ghost"
          aria-label={t('expiryRefreshQuota')}
          isPending={refreshing}
          onPress={onRefresh}
        >
          <ArrowClockwise size={14} />
        </Button>
      </td>
    </tr>
  )
}

/** 到期明细表：四组（主窗口内 / 次窗口内 / 更远 / 未上报）+ 行展开包明细 + 行操作。 */export function ExpiryTable({
  groups,
  collapsed,
  onToggleGroup,
  highlightId,
  refreshingId,
  onRefresh,
  now,
  t,
}: Props) {
  const [openId, setOpenId] = useState('')
  const total = EXPIRY_GROUP_ORDER.reduce((count, group) => count + groups[group].length, 0)

  if (total === 0) {
    return (
      <EmptyPanel
        className="min-h-40 rounded-2xl border border-dashed border-border"
        title={t('expiryEmpty')}
        hint={t('expiryEmptyHint')}
      />
    )
  }

  return (
    <div className="space-y-4">
      {EXPIRY_GROUP_ORDER.map((group) => {
        const rows = groups[group]
        if (!rows.length) return null
        const open = !collapsed[group]
        return (
          <section key={group} className="overflow-hidden rounded-2xl border border-border bg-surface">
            <button
              type="button"
              onClick={() => onToggleGroup(group)}
              aria-expanded={open}
              className="flex w-full items-center gap-2 border-b border-separator px-4 py-2.5 text-left"
            >
              {open ? <CaretDown size={13} className="text-muted" /> : <CaretRight size={13} className="text-muted" />}
              <span className="text-sm font-semibold">{t(expiryGroupLabelKey(group))}</span>
              <span className="mono text-xs text-muted">{rows.length}</span>
            </button>
            {open ? (
              <div className="overflow-x-auto">
                <table className="w-full border-collapse">
                  <thead>
                    <tr className="text-micro font-medium tracking-[0.08em] text-muted uppercase">
                      <th className="px-3 py-2 text-left font-medium">{t('expiryColAccount')}</th>
                      <th className="px-3 py-2 text-left font-medium">{t('expiryColProvider')}</th>
                      <th className="px-3 py-2 text-left font-medium">{t('expiryColExpiresAt')}</th>
                      <th className="px-3 py-2 text-right font-medium">{t('expiryColRemaining')}</th>
                      <th className="px-3 py-2 text-right font-medium">{t('expiryColPackages')}</th>
                      <th className="px-3 py-2 text-right font-medium">{t('expiryColActions')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row) => (
                      <Fragment key={row.id}>
                        <ExpiryRowCells
                          row={row}
                          open={openId === row.id}
                          highlight={highlightId === row.id}
                          refreshing={refreshingId === row.id}
                          onToggle={() => setOpenId((current) => (current === row.id ? '' : row.id))}
                          onRefresh={() => onRefresh(row.id)}
                          now={now}
                          t={t}
                        />
                        {openId === row.id ? (
                          <tr className="border-t border-separator bg-surface-secondary/40">
                            <td colSpan={6} className="px-3 py-2">
                              <PackageRows packages={row.packages} t={t} />
                            </td>
                          </tr>
                        ) : null}
                      </Fragment>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}
          </section>
        )
      })}
    </div>
  )
}
