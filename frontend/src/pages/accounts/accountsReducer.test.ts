import { describe, expect, it } from 'vitest'

import {
  accountsReducer,
  initialAccountsUiState,
  type AccountsUiState,
} from '@/pages/accounts/accountsReducer'

describe('accountsReducer', () => {
  it('模态开关：panels 各键独立（不同账号可同时打开各自模态）', () => {
    const opened = accountsReducer(initialAccountsUiState, { type: 'panel', panel: 'models', id: 'a1' })
    const both = accountsReducer(opened, { type: 'panel', panel: 'confirm', id: 'a2' })
    expect(both.panels.models).toBe('a1')
    expect(both.panels.confirm).toBe('a2')
    expect(both.panels.edit).toBeNull()

    const closed = accountsReducer(both, { type: 'panel', panel: 'models', id: null })
    expect(closed.panels.models).toBeNull()
    expect(closed.panels.confirm).toBe('a2')
  })

  it('addOpen / authPanel：添加向导与卡片登录面板各自可控', () => {
    const added = accountsReducer(initialAccountsUiState, { type: 'addOpen', open: true })
    expect(added.addOpen).toBe(true)
    expect(accountsReducer(added, { type: 'authPanel', id: 'a1' }).authPanelId).toBe('a1')
    expect(accountsReducer(added, { type: 'authPanel', id: null }).authPanelId).toBeNull()
  })

  it('busy：设置与清除（同刻只保留一个在途标记）', () => {
    const busy = accountsReducer(initialAccountsUiState, { type: 'busy', busy: { id: 'a1', kind: 'refresh' } })
    expect(busy.busy).toEqual({ id: 'a1', kind: 'refresh' })
    expect(accountsReducer(busy, { type: 'busy', busy: null }).busy).toBeNull()
  })

  it('transient：patch 合并保留其他键（提示更新不清掉已粘贴的 PAT）', () => {
    const withPat = accountsReducer(initialAccountsUiState, { type: 'transient', id: 'a1', patch: { pat: 'token' } })
    const withNote = accountsReducer(withPat, { type: 'transient', id: 'a1', patch: { note: '等待中' } })
    expect(withNote.transients.a1).toEqual({ pat: 'token', note: '等待中' })
  })

  it('clearTransient：只清指定键；记录清空即删除', () => {
    let state: AccountsUiState = initialAccountsUiState
    state = accountsReducer(state, { type: 'transient', id: 'a1', patch: { pat: 'token', note: '等待中', authUrl: 'https://x' } })
    state = accountsReducer(state, { type: 'clearTransient', id: 'a1', keys: ['note'] })
    expect(state.transients.a1).toEqual({ pat: 'token', authUrl: 'https://x' })
    state = accountsReducer(state, { type: 'clearTransient', id: 'a1', keys: ['pat', 'authUrl'] })
    expect(state.transients.a1).toBeUndefined()
    // 无记录时不产生新对象
    expect(accountsReducer(state, { type: 'clearTransient', id: 'a1', keys: ['note'] })).toBe(state)
  })

  it('override：乐观覆盖可多次合并；清除后回到服务端数据', () => {
    let state = accountsReducer(initialAccountsUiState, { type: 'override', id: 'a1', patch: { enabled: false } })
    state = accountsReducer(state, { type: 'override', id: 'a1', patch: { name: '新名字' } })
    expect(state.overrides.a1).toEqual({ enabled: false, name: '新名字' })
    state = accountsReducer(state, { type: 'clearOverride', id: 'a1' })
    expect(state.overrides.a1).toBeUndefined()
    expect(accountsReducer(state, { type: 'clearOverride', id: 'a1' })).toBe(state)
  })

  it('forget：删除账号后清掉其临时态 / 覆盖 / 在途 / 面板与登录面板指向', () => {
    let state: AccountsUiState = initialAccountsUiState
    state = accountsReducer(state, { type: 'transient', id: 'a1', patch: { note: 'x' } })
    state = accountsReducer(state, { type: 'override', id: 'a1', patch: { name: 'x' } })
    state = accountsReducer(state, { type: 'busy', busy: { id: 'a1', kind: 'delete' } })
    state = accountsReducer(state, { type: 'panel', panel: 'edit', id: 'a1' })
    state = accountsReducer(state, { type: 'panel', panel: 'confirm', id: 'a2' })
    state = accountsReducer(state, { type: 'authPanel', id: 'a1' })
    state = accountsReducer(state, { type: 'transient', id: 'a2', patch: { note: '其他账号' } })

    const next = accountsReducer(state, { type: 'forget', id: 'a1' })
    expect(next.transients.a1).toBeUndefined()
    expect(next.overrides.a1).toBeUndefined()
    expect(next.busy).toBeNull()
    expect(next.panels.edit).toBeNull()
    expect(next.authPanelId).toBeNull()
    // 其他账号与未指向它的面板不受影响
    expect(next.panels.confirm).toBe('a2')
    expect(next.transients.a2).toEqual({ note: '其他账号' })
  })
})
