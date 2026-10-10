import { Button } from '@heroui/react'
import { X } from '@phosphor-icons/react'

import { StatusDot } from '@/components/ui/StatusDot'
import type { AccountRow } from '@/lib/account'

import { WiredAccountRow, type RowWiring } from './RowWiring'

type Props = {
  /** 'attn' = 需关注（危险态）；'transit' = 在途（冷却）。 */
  kind: 'attn' | 'transit'
  title: string
  rows: AccountRow[]
  onClear: () => void
  wiring: RowWiring
}

/**
 * 「需关注 / 在途」行动视图（设计 D13）：跨渠道扁平列表、按严重度排序、
 * 行与分组列表同规格（同一 WiredAccountRow，含完整展开详情与操作）。
 */
export function AccountsActionView({ kind, title, rows, onClear, wiring }: Props) {
  return (
    <section className="action-view" data-gsap-reveal>
      <div className="action-head">
        <StatusDot state={kind === 'attn' ? 'danger' : 'warn'} />
        <span className="text-sm font-semibold">{title} · <span className="mono">{rows.length}</span></span>
        <span className="text-micro text-tertiary">{wiring.t('actionViewHint')}</span>
        <span className="spacer" />
        <Button size="sm" variant="ghost" onPress={onClear}>
          <X size={13} />{wiring.t('clearFilters')}
        </Button>
      </div>
      <div className="action-rows">
        {rows.map((account) => <WiredAccountRow key={account.id} account={account} wiring={wiring} />)}
      </div>
    </section>
  )
}
