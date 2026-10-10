import type { Dispatch } from 'react'

import type { AccountBusyKind } from '@/components/accounts/AccountRow'
import type { AccountPool } from '@/components/accounts/useAccountPool'
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
  /** 当前打开的认证模态账号（用于「再次点击 = 关闭」与取消轮询）。 */
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
  onViewModels: (id: string) => void
  onViewCheckins: (id: string) => void
  /** 打开认证模态（⋯ 更多操作菜单 → 认证方式）。 */
  onToggleAuthPanel: (id: string) => void
  /** ⋯ 菜单：打开重命名模态。 */
  onRename: (id: string) => void
  /** ⋯ 菜单：打开设置（优先级）模态。 */
  onOpenSettings: (id: string) => void
  /** 重命名提交。 */
  onSaveName: (id: string, name: string) => Promise<void>
  /** 优先级提交（设置模态）。 */
  onSavePriority: (id: string, priority: number) => Promise<void>
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
      dispatch({ type: 'panel', panel: 'auth', id: null })
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
    // 打开认证模态。关闭（切换 / 关闭时）取消该账号的等待轮询。
    const opening = authPanelId !== id
    if (!opening && deviceLogin.accountId === id) deviceLogin.cancel()
    dispatch({ type: 'panel', panel: 'auth', id: opening ? id : null })
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
    onViewModels: (id) => dispatch({ type: 'panel', panel: 'models', id }),
    onViewCheckins: (id) => dispatch({ type: 'panel', panel: 'checkins', id }),
    onToggleAuthPanel,
    onRename: (id) => dispatch({ type: 'panel', panel: 'rename', id }),
    onOpenSettings: (id) => dispatch({ type: 'panel', panel: 'settings', id }),
    onSaveName: async (id, name) => {
      dispatch({ type: 'override', id, patch: { name } })
      try {
        await pool.saveSettings(id, { name })
      } finally {
        dispatch({ type: 'clearOverride', id })
      }
    },
    onSavePriority: async (id, priority) => {
      const next = Math.min(100, Math.max(1, Math.trunc(priority)))
      dispatch({ type: 'override', id, patch: { priority: next } })
      try {
        await pool.saveSettings(id, { priority: next })
      } finally {
        dispatch({ type: 'clearOverride', id })
      }
    },
  }
}
