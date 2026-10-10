import type { ProviderDescriptor } from '@/api/overview'

import { AccountRowItem, type AccountBusyKind } from '@/components/accounts/AccountRow'
import type { Translate } from '@/i18n/messages'
import type { AccountRow } from '@/lib/account'

import { authCapabilitiesFor, checkinPolicyFor, hasLoginAction } from './accountPolicy'
import type { AccountHandlers } from './accountActions'
import type { AccountTransient } from './accountsReducer'

/** 行组件装配参数（分组列表 / 行动视图共用，避免双份接线漂移）。 */
export type RowWiring = {
  busyKindFor: (id: string) => AccountBusyKind | ''
  transients: Record<string, AccountTransient>
  providers: ProviderDescriptor[]
  handlers: AccountHandlers
  t: Translate
  /** 收件箱跳转目标（`?focus=`）：匹配行高亮。 */
  focusId?: string
}

/** 把行、临时态与动作集合装配到 AccountRowItem（能力门控与旧网格/wiring 保持一致）。 */
export function WiredAccountRow({ account, wiring }: { account: AccountRow; wiring: RowWiring }) {
  const capabilities = authCapabilitiesFor(wiring.providers, account)
  const checkinPolicy = checkinPolicyFor(wiring.providers, account)
  return (
    <AccountRowItem
      account={account}
      busyKind={wiring.busyKindFor(account.id)}
      focus={wiring.focusId === account.id}
      t={wiring.t}
      onExport={() => wiring.handlers.onExport(account.id)}
      onRefresh={() => wiring.handlers.onRefresh(account.id)}
      onDelete={() => wiring.handlers.onDelete(account.id)}
      onToggle={(selected) => wiring.handlers.onToggle(account.id, selected)}
      onToggleModelRequests={(selected) => wiring.handlers.onToggleModelRequests(account.id, selected)}
      onToggleAutoCheckin={checkinPolicy ? (selected) => wiring.handlers.onToggleAutoCheckin(account.id, selected) : undefined}
      onCheckin={checkinPolicy ? () => wiring.handlers.onCheckin(account.id) : undefined}
      onViewCheckins={checkinPolicy ? () => wiring.handlers.onViewCheckins(account.id) : undefined}
      onClearCooldowns={() => wiring.handlers.onClearCooldowns(account.id)}
      onRename={() => wiring.handlers.onRename(account.id)}
      onOpenSettings={() => wiring.handlers.onOpenSettings(account.id)}
      onToggleAuthPanel={hasLoginAction(wiring.providers, account) || capabilities !== null ? () => wiring.handlers.onToggleAuthPanel(account.id) : undefined}
      onViewModels={() => wiring.handlers.onViewModels(account.id)}
    />
  )
}
