import type { Dispatch } from 'react'

import type { AccountBusyKind } from '@/components/accounts/AccountRow'
import type { AccountPool, AccountSettingsInput } from '@/components/accounts/useAccountPool'
import type { DeviceLoginPoll } from '@/components/accounts/useDeviceLoginPoll'
import type { Translate } from '@/i18n/messages'
import { copyText } from '@/lib/clipboard'

import type { AccountTransient, AccountsUiAction } from './accountsReducer'

/** 单账号动作的执行器（useAsyncAction 的入参）：
 *  pending 中重复调用被 useAsyncAction 去重；true=成功 / false=失败（note 已落）。 */
export type RunAction = (
  id: string,
  kind: AccountBusyKind,
  fn: () => Promise<unknown>,
) => Promise<boolean | undefined>

export type AccountHandlersInput = {
  dispatch: Dispatch<AccountsUiAction>
  authPanelId: string | null
  transients: Record<string, AccountTransient>
  pool: AccountPool
  run: RunAction
  deviceLogin: DeviceLoginPoll
  t: Translate
}

export type AccountHandlers = {
  onPatChange: (id: string, value: string) => void
  onCallbackChange: (id: string, value: string) => void
  onDeviceLogin: (id: string) => void
  onPat: (id: string) => void
  onCallback: (id: string) => void
  onExport: (id: string) => void
  onRefresh: (id: string) => void
  onToggle: (id: string, selected: boolean) => void
  onToggleAutoCheckin: (id: string, selected: boolean) => void
  onToggleModelRequests: (id: string, selected: boolean) => void
  onCheckin: (id: string) => void
  onClearCooldowns: (id: string) => void
  onDelete: (id: string) => void
  onEdit: (id: string) => void
  onViewModels: (id: string) => void
  onViewCheckins: (id: string) => void
  onToggleAuthPanel: (id: string) => void
  /** 抛错（EditAccountModal 自行展示错误），成功与否由 promise 决定。 */
  onSaveSettings: (id: string, input: AccountSettingsInput) => Promise<void>
}

/** useAsyncAction 的执行体：统一在途标记与失败文案（迁移前 run() 模式）。 */
export function createAccountRunner(dispatch: Dispatch<AccountsUiAction>) {
  return async (id: string, kind: AccountBusyKind, fn: () => Promise<unknown>): Promise<boolean> => {
    dispatch({ type: 'busy', busy: { id, kind } })
    dispatch({ type: 'clearTransient', id, keys: ['note'] })
    try {
      await fn()
      return true
    } catch (error) {
      dispatch({
        type: 'transient',
        id,
        patch: { note: error instanceof Error ? error.message : String(error) },
      })
      return false
    } finally {
      dispatch({ type: 'busy', busy: null })
    }
  }
}

/** 账号卡片 / 模态的动作集合（页面组装，AccountCard 直接消费）。 */
export function createAccountHandlers({
  dispatch,
  authPanelId,
  transients,
  pool,
  run,
  deviceLogin,
  t,
}: AccountHandlersInput): AccountHandlers {
  const patch = (id: string, value: AccountTransient) => dispatch({ type: 'transient', id, patch: value })

  async function onDeviceLogin(id: string) {
    dispatch({ type: 'authPanel', id })
    await run(id, 'device', async () => {
      patch(id, { note: t('wizardStartingSession'), authUrl: '' })
      const output = await pool.startDeviceLogin(id)
      if (output.authUrl) {
        patch(id, { authUrl: output.authUrl })
        window.open(output.authUrl, '_blank', 'noopener,noreferrer')
      }
      patch(id, { note: t('waitingLogin') })
      deviceLogin.start(id)
    })
  }

  async function onCallback(id: string) {
    const pasted = (transients[id]?.callback || '').trim()
    if (!pasted) {
      patch(id, { note: t('wizardCallbackPh') })
      return
    }
    const ok = await run(id, 'callback', async () => {
      patch(id, { note: t('wizardStartingSession') })
      await pool.submitCallback(id, pasted)
    })
    if (ok) {
      dispatch({ type: 'authPanel', id: null })
      patch(id, { callback: '', note: '' })
    }
  }

  async function onPat(id: string) {
    const token = (transients[id]?.pat || '').trim()
    if (!token) {
      patch(id, { note: t('pastePatFirst') })
      return
    }
    await run(id, 'pat', async () => {
      patch(id, { note: t('wizardStartingSession') })
      await pool.loginPat(id, token)
      dispatch({ type: 'clearTransient', id, keys: ['pat'] })
    })
  }

  function onExport(id: string) {
    void run(id, 'export', async () => {
      const bundle = await pool.exportCredentials(id)
      await copyText(JSON.stringify(bundle, null, 2))
      patch(id, { note: t('credentialCopied') })
    })
  }

  function onToggle(id: string, selected: boolean) {
    dispatch({ type: 'override', id, patch: { enabled: selected } })
    void run(id, 'toggle', () => pool.setEnabled(id, selected)).finally(() => {
      dispatch({ type: 'clearOverride', id })
    })
  }

  function onToggleAutoCheckin(id: string, selected: boolean) {
    void run(id, 'toggle', () => pool.setAutoCheckin(id, selected))
  }

  function onToggleModelRequests(id: string, selected: boolean) {
    void run(id, 'toggle', () => pool.setModelRequests(id, selected))
  }

  function onDelete(id: string) {
    void run(id, 'delete', async () => {
      await pool.remove(id)
      dispatch({ type: 'forget', id })
    })
  }

  function onToggleAuthPanel(id: string) {
    const closing = authPanelId === id
    // 关闭登录面板 = 取消该账号的等待轮询（迁移前排询会继续到上限）。
    if (closing && deviceLogin.accountId === id) deviceLogin.cancel()
    dispatch({ type: 'authPanel', id: closing ? null : id })
  }

  async function onSaveSettings(id: string, input: AccountSettingsInput) {
    if (!id) throw new Error(t('accountNameRequired'))
    dispatch({
      type: 'override',
      id,
      patch: { name: input.name, max_inflight: input.max_inflight, priority: input.priority },
    })
    dispatch({ type: 'busy', busy: { id, kind: 'settings' } })
    try {
      await pool.saveSettings(id, input)
    } finally {
      dispatch({ type: 'busy', busy: null })
      dispatch({ type: 'clearOverride', id })
    }
  }

  return {
    onPatChange: (id, value) => patch(id, { pat: value }),
    onCallbackChange: (id, value) => patch(id, { callback: value }),
    onDeviceLogin: (id) => void onDeviceLogin(id),
    onPat: (id) => void onPat(id),
    onCallback: (id) => void onCallback(id),
    onExport,
    onRefresh: (id) => void run(id, 'refresh', async () => {
      const { failed } = await pool.refreshQuota([id])
      if (failed.length) throw new Error(failed[0].message)
    }),
    onToggle,
    onToggleAutoCheckin,
    onToggleModelRequests,
    onCheckin: (id) => void run(id, 'checkin', () => pool.checkin(id)),
    onClearCooldowns: (id) => void run(id, 'cooldowns', () => pool.clearCooldowns(id)),
    onDelete,
    onEdit: (id) => dispatch({ type: 'panel', panel: 'edit', id }),
    onViewModels: (id) => dispatch({ type: 'panel', panel: 'models', id }),
    onViewCheckins: (id) => dispatch({ type: 'panel', panel: 'checkins', id }),
    onToggleAuthPanel,
    onSaveSettings,
  }
}
