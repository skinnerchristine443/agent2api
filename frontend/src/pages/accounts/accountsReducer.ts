import type { AccountBusyKind } from '@/components/accounts/AccountRow'
import type { AccountRow } from '@/lib/account'

/** 单账号操作的在途标记（同刻只保留一个；与迁移前 busy 语义一致）。 */
export type AccountBusy = { id: string; kind: AccountBusyKind }

/**
 * 账号的临时输入 / 提示（登录面板内粘贴的内容与状态文案）。
 * 合并为一张表：4 个 useState map → 1 个记录（reducer 内按键清理）。
 */
export type AccountTransient = {
  note?: string
  pat?: string
  callback?: string
  authUrl?: string
}

/** 写操作期间的乐观覆盖（服务端数据回来前先让卡片显示新值）。 */
export type AccountOverride = Partial<Pick<AccountRow, 'enabled' | 'name' | 'max_inflight' | 'priority'>>

/** 页面上的 4 个账号模态（添加向导由 addOpen 单独承载）。 */
export const ACCOUNT_PANELS = ['edit', 'models', 'checkins', 'confirm'] as const
export type AccountPanelKey = (typeof ACCOUNT_PANELS)[number]

export type AccountsUiState = {
  addOpen: boolean
  /** 卡片内联登录面板（同刻仅一个账号展开）。 */
  authPanelId: string | null
  /** 打开中的模态 → 目标账号 id。 */
  panels: Record<AccountPanelKey, string | null>
  busy: AccountBusy | null
  transients: Record<string, AccountTransient>
  overrides: Record<string, AccountOverride>
}

export const initialAccountsUiState: AccountsUiState = {
  addOpen: false,
  authPanelId: null,
  panels: { edit: null, models: null, checkins: null, confirm: null },
  busy: null,
  transients: {},
  overrides: {},
}

export type AccountsUiAction =
  | { type: 'addOpen'; open: boolean }
  | { type: 'authPanel'; id: string | null }
  | { type: 'panel'; panel: AccountPanelKey; id: string | null }
  | { type: 'busy'; busy: AccountBusy | null }
  | { type: 'transient'; id: string; patch: AccountTransient }
  | { type: 'clearTransient'; id: string; keys: Array<keyof AccountTransient> }
  | { type: 'override'; id: string; patch: AccountOverride }
  | { type: 'clearOverride'; id: string }
  /** 账号已删除：清掉它的临时输入 / 覆盖 / 在途与面板指向。 */
  | { type: 'forget'; id: string }

function patchTransient(state: AccountsUiState, id: string, patch: AccountTransient): AccountsUiState {
  return {
    ...state,
    transients: { ...state.transients, [id]: { ...state.transients[id], ...patch } },
  }
}

function clearTransientKeys(
  state: AccountsUiState,
  id: string,
  keys: Array<keyof AccountTransient>,
): AccountsUiState {
  const current = state.transients[id]
  if (!current) return state
  const next: AccountTransient = { ...current }
  for (const key of keys) delete next[key]
  const transients = { ...state.transients }
  if (Object.keys(next).length) transients[id] = next
  else delete transients[id]
  return { ...state, transients }
}

function forgetAccount(state: AccountsUiState, id: string): AccountsUiState {
  const next: AccountsUiState = { ...state }
  if (state.transients[id]) {
    const transients = { ...state.transients }
    delete transients[id]
    next.transients = transients
  }
  if (state.overrides[id]) {
    const overrides = { ...state.overrides }
    delete overrides[id]
    next.overrides = overrides
  }
  if (state.busy?.id === id) next.busy = null
  if (state.authPanelId === id) next.authPanelId = null
  const panels = { ...state.panels }
  let panelsChanged = false
  for (const key of ACCOUNT_PANELS) {
    if (panels[key] === id) {
      panels[key] = null
      panelsChanged = true
    }
  }
  if (panelsChanged) next.panels = panels
  return next
}

/**
 * 账号池 UI 状态机（迁移前 ~20 个 useState）：
 * 模态 / 在途 / 临时输入 / 乐观覆盖四类，筛选与分页已进 URL（不在此处）。
 */
export function accountsReducer(state: AccountsUiState, action: AccountsUiAction): AccountsUiState {
  switch (action.type) {
    case 'addOpen':
      return { ...state, addOpen: action.open }
    case 'authPanel':
      return { ...state, authPanelId: action.id }
    case 'panel':
      return { ...state, panels: { ...state.panels, [action.panel]: action.id } }
    case 'busy':
      return { ...state, busy: action.busy }
    case 'transient':
      return patchTransient(state, action.id, action.patch)
    case 'clearTransient':
      return clearTransientKeys(state, action.id, action.keys)
    case 'override':
      return { ...state, overrides: { ...state.overrides, [action.id]: { ...state.overrides[action.id], ...action.patch } } }
    case 'clearOverride': {
      if (!state.overrides[action.id]) return state
      const overrides = { ...state.overrides }
      delete overrides[action.id]
      return { ...state, overrides }
    }
    case 'forget':
      return forgetAccount(state, action.id)
    default:
      return state
  }
}
