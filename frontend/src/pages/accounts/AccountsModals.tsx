import { AccountCheckinRecordsModal } from '@/components/accounts/AccountCheckinRecordsModal'
import { AccountModelsModal } from '@/components/accounts/AccountModelsModal'
import { AddAccountModal } from '@/components/accounts/add-account/AddAccountModal'
import { AccountAuthModal } from '@/components/accounts/AccountAuthModal'
import { AccountSettingsModal } from '@/components/accounts/AccountSettingsModal'
import { RenameAccountModal } from '@/components/accounts/RenameAccountModal'
import type { ProviderDescriptor } from '@/api/overview'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import type { Translate } from '@/i18n/messages'
import type { AccountRow } from '@/lib/account'

import type { AccountHandlers } from './accountActions'
import { authCapabilitiesFor } from './accountPolicy'
import type { AccountBusy, AccountPanelKey, AccountTransient } from './accountsReducer'

type Props = {
  /** 打开中的模态 → 目标账号 id（null = 关闭）。 */
  panels: Record<AccountPanelKey, string | null>
  rows: AccountRow[]
  busy: AccountBusy | null
  addOpen: boolean
  /** 添加向导预选渠道（批次 9：账号池页签联动）。 */
  addPresetProvider?: string
  providers: ProviderDescriptor[]
  t: Translate
  onAddOpenChange: (open: boolean) => void
  onAdded: () => void
  onClosePanel: (panel: AccountPanelKey) => void
  onConfirmDelete: (id: string) => void
  /** 认证模态的临时输入 / 结果（按账号）。 */
  transients: Record<string, AccountTransient>
  handlers: AccountHandlers
}

/** 账号池的 5 个模态装配（添加向导 / 编辑 / 模型 / 签到记录 / 删除确认）。 */
export function AccountsModals({
  panels,
  rows,
  busy,
  addOpen,
  addPresetProvider,
  providers,
  t,
  onAddOpenChange,
  onAdded,
  onClosePanel,
  onConfirmDelete,
  transients,
  handlers,
}: Props) {
  const accountOf = (id: string | null) => rows.find((account) => account.id === id) || null
  const confirmAccount = accountOf(panels.confirm)
  const modelsAccount = accountOf(panels.models)
  const checkinAccount = accountOf(panels.checkins)
  const renameAccount = accountOf(panels.rename)
  const renameId = panels.rename || ''
  const settingsAccount = accountOf(panels.settings)
  const settingsId = panels.settings || ''
  const authAccount = accountOf(panels.auth)
  const authId = panels.auth || ''
  const authTransient = authId ? transients[authId] || {} : {}
  const authCapabilities = authAccount ? authCapabilitiesFor(providers, authAccount) : null

  return (
    <>
      <AddAccountModal
        isOpen={addOpen}
        presetProvider={addPresetProvider}
        onClose={() => onAddOpenChange(false)}
        onAdded={onAdded}
      />
      {/* key 加面板前缀：三者同时关闭时均为 `closed`，若不加前缀会同级重复 key（React 告警）。 */}
      <AccountModelsModal
        key={`models:${panels.models ?? 'closed'}`}
        account={modelsAccount}
        t={t}
        onClose={() => onClosePanel('models')}
      />
      <AccountCheckinRecordsModal
        key={`checkins:${panels.checkins ?? 'closed'}`}
        account={checkinAccount}
        t={t}
        onClose={() => onClosePanel('checkins')}
      />
      <AccountAuthModal
        key={`auth:${panels.auth ?? 'closed'}`}
        account={authAccount}
        busyKind={busy && busy.id === authId ? busy.kind : ''}
        t={t}
        authUrl={authTransient.authUrl}
        note={authTransient.note}
        pat={authTransient.pat || ''}
        onPatChange={(value) => handlers.onPatChange(authId, value)}
        onDeviceLogin={authCapabilities?.browser_login !== false ? () => handlers.onDeviceLogin(authId) : undefined}
        onPatLogin={authCapabilities?.pat_login !== false ? () => handlers.onPat(authId) : undefined}
        callbackUrl={authTransient.callback || ''}
        onCallbackChange={(value) => handlers.onCallbackChange(authId, value)}
        onSubmitCallback={() => handlers.onCallback(authId)}
        onClose={() => onClosePanel('auth')}
      />
      <RenameAccountModal
        key={`rename:${panels.rename ?? 'closed'}`}
        account={renameAccount}
        busy={Boolean(busy && busy.id === renameId && busy.kind === 'settings')}
        t={t}
        onClose={() => onClosePanel('rename')}
        onSave={(name) => handlers.onSaveName(renameId, name)}
      />
      <AccountSettingsModal
        key={`settings:${panels.settings ?? 'closed'}`}
        account={settingsAccount}
        busy={Boolean(busy && busy.id === settingsId && busy.kind === 'settings')}
        t={t}
        onClose={() => onClosePanel('settings')}
        onSave={(priority) => handlers.onSavePriority(settingsId, priority)}
      />
      <ConfirmDialog
        isOpen={Boolean(confirmAccount)}
        title={t('delete')}
        description={t('deleteAccountConfirm', { name: confirmAccount?.name || confirmAccount?.id || '' })}
        confirmLabel={t('delete')}
        cancelLabel={t('cancel')}
        closeLabel={t('close')}
        isPending={Boolean(busy && busy.id === panels.confirm && busy.kind === 'delete')}
        onClose={() => onClosePanel('confirm')}
        onConfirm={() => {
          if (confirmAccount) onConfirmDelete(confirmAccount.id)
        }}
      />
    </>
  )
}
