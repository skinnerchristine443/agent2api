import { AccountCheckinRecordsModal } from '@/components/accounts/AccountCheckinRecordsModal'
import { AccountModelsModal } from '@/components/accounts/AccountModelsModal'
import { AddAccountModal } from '@/components/accounts/add-account/AddAccountModal'
import { EditAccountModal } from '@/components/accounts/EditAccountModal'
import type { AccountSettingsInput } from '@/components/accounts/useAccountPool'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import type { Translate } from '@/i18n/messages'
import type { AccountRow } from '@/lib/account'

import type { AccountBusy, AccountPanelKey } from './accountsReducer'

type Props = {
  /** 打开中的模态 → 目标账号 id（null = 关闭）。 */
  panels: Record<AccountPanelKey, string | null>
  rows: AccountRow[]
  busy: AccountBusy | null
  addOpen: boolean
  /** 添加向导预选渠道（批次 9：账号池页签联动）。 */
  addPresetProvider?: string
  t: Translate
  onAddOpenChange: (open: boolean) => void
  onAdded: () => void
  onClosePanel: (panel: AccountPanelKey) => void
  onConfirmDelete: (id: string) => void
  onSaveSettings: (id: string, input: AccountSettingsInput) => Promise<void>
}

/** 账号池的 5 个模态装配（添加向导 / 编辑 / 模型 / 签到记录 / 删除确认）。 */
export function AccountsModals({
  panels,
  rows,
  busy,
  addOpen,
  addPresetProvider,
  t,
  onAddOpenChange,
  onAdded,
  onClosePanel,
  onConfirmDelete,
  onSaveSettings,
}: Props) {
  const accountOf = (id: string | null) => rows.find((account) => account.id === id) || null
  const confirmAccount = accountOf(panels.confirm)
  const modelsAccount = accountOf(panels.models)
  const checkinAccount = accountOf(panels.checkins)
  const editAccount = accountOf(panels.edit)
  const editId = panels.edit

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
      <EditAccountModal
        key={`edit:${panels.edit ?? 'closed'}`}
        account={editAccount}
        busy={Boolean(busy && busy.id === editId && busy.kind === 'settings')}
        t={t}
        onClose={() => onClosePanel('edit')}
        onSave={(input) => onSaveSettings(editId || '', input)}
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
