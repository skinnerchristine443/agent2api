import { useEffect, useRef, useState, type ChangeEvent } from 'react'

import type { ImportBatchItem } from '@/api/overview'
import { useDeviceLoginPoll, type DeviceLoginOutcome } from '@/components/accounts/useDeviceLoginPoll'
import { useI18n } from '@/hooks/I18nContext'

import {
  startBrowserFlow,
  submitCallbackFlow,
  submitPatFlow,
  type WizardFlowContext,
} from './addAccountFlows'
import { submitImport } from './importFlow'
import { loadProviderOptions, optionHint, type ProviderOption } from './providerOptions'
import type { AddAccountPhase, AddAccountStep, AddAccountTab } from './types'

/** 浏览器登录等待上限（迁移前：90 次 × 2s；间隔由 useDeviceLoginPoll 提供）。 */
const DEVICE_LOGIN_ATTEMPTS = 90

export type AddAccountWizard = ReturnType<typeof useAddAccountWizard>

/**
 * 添加账号向导的状态机（视图与逻辑分离）：
 * 类型选择 → 浏览器（设备码，`useDeviceLoginPoll`）/ PAT / 凭证导入（含批量逐项报告）。
 * 关闭语义：busy（创建/启动在途）不可关闭；polling 可直接关闭并取消等待。
 * `presetProvider`（批次 9）：打开时预选渠道（账号池页签联动），不存在则回落首项。
 */
export function useAddAccountWizard({ isOpen, onClose, onAdded, presetProvider }: {
  isOpen: boolean
  onClose: () => void
  onAdded: () => void
  presetProvider?: string
}) {
  const { t } = useI18n()
  const [step, setStep] = useState<AddAccountStep>('method')
  const [tab, setTab] = useState<AddAccountTab>('browser')
  const [accountType, setAccountType] = useState('')
  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>([])
  const [typesLoading, setTypesLoading] = useState(false)
  const [name, setName] = useState('')
  const [pat, setPat] = useState('')
  const [json, setJson] = useState('')
  const [batchRows, setBatchRows] = useState<ImportBatchItem[]>([])
  const [phase, setPhase] = useState<AddAccountPhase>('idle')
  const [message, setMessage] = useState('')
  const [authUrl, setAuthUrl] = useState('')
  const [callbackUrl, setCallbackUrl] = useState('')
  const createdId = useRef<string>('')
  const fileInput = useRef<HTMLInputElement>(null)

  const deviceLogin = useDeviceLoginPoll({ attempts: DEVICE_LOGIN_ATTEMPTS, onFinish: onPollFinish })
  const cancelPoll = deviceLogin.cancel
  // 预选渠道只在「打开」瞬间读取（打开期间切换页签不可达，模态遮挡）；
  // 用 ref 承接避免把 presetProvider 拖进打开 effect 的依赖。
  const presetRef = useRef(presetProvider)
  useEffect(() => {
    presetRef.current = presetProvider
  }, [presetProvider])

  useEffect(() => {
    if (!isOpen) return
    let cancelled = false
    setTypesLoading(true)
    setProviderOptions([])
    setAccountType('')
    setBatchRows([])
    void loadProviderOptions()
      .then((options) => {
        if (cancelled) return
        // 页签联动（批次 9；15 起 preset 为渠道键 `<provider>-<region>`）：
        // 先按 option.id 精确匹配（workbuddy-cn），再退化按 provider 匹配（旧调用方）。
        const preset = presetRef.current
        const preferred = preset
          ? options.find((option) => option.id === preset) || options.find((option) => option.provider === preset)
          : undefined
        const ordered = preferred ? [preferred, ...options.filter((option) => option !== preferred)] : options
        setProviderOptions(ordered)
        setAccountType(ordered[0]?.id || '')
        setTab('browser')
      })
      .finally(() => {
        if (!cancelled) setTypesLoading(false)
      })
    return () => { cancelled = true }
  }, [isOpen])

  // 关闭模态即停（含 Esc / 遮罩关闭）。
  useEffect(() => {
    if (isOpen) return
    cancelPoll()
  }, [cancelPoll, isOpen])

  const activeOption = providerOptions.find((option) => option.id === accountType)
  const typesReady = Boolean(activeOption) && !typesLoading
  const showPatTab = activeOption?.descriptor.capabilities?.pat_login !== false
  const showImportTab = activeOption?.descriptor.capabilities?.import_export !== false
  const hasBrowserLogin = activeOption?.descriptor.capabilities?.browser_login !== false
  const showCallbackPaste = activeOption?.provider === 'trae'
  const busy = phase === 'busy' || phase === 'polling'
  const hint = optionHint(activeOption, t)

  useEffect(() => {
    if (tab === 'pat' && !showPatTab) setTab('browser')
    if (tab === 'import' && !showImportTab) setTab('browser')
  }, [showImportTab, showPatTab, tab])

  useEffect(() => {
    // 不支持浏览器登录的 provider 不应落到空的浏览器标签页；
    // 改为把运维人员送到粘贴 key 的标签页。
    if (hasBrowserLogin) return
    if (showPatTab) setTab('pat')
    else if (showImportTab) setTab('import')
  }, [hasBrowserLogin, showImportTab, showPatTab])

  function reset() {
    cancelPoll()
    setStep('method')
    setTab('browser')
    setAccountType('')
    setProviderOptions([])
    setTypesLoading(false)
    setName('')
    setPat('')
    setJson('')
    setPhase('idle')
    setMessage('')
    setAuthUrl('')
    setCallbackUrl('')
    createdId.current = ''
  }

  function finishAndClose() {
    reset()
    onClose()
  }

  function close() {
    if (phase === 'busy') return
    if (phase === 'polling') {
      // 取消等待：停轮询并让已创建的账号回列表可见。
      cancelPoll()
      onAdded()
    }
    reset()
    onClose()
  }

  function onPollFinish(outcome: DeviceLoginOutcome, _accountId: string, loginMessage: string) {
    if (outcome === 'cancelled') return
    if (outcome === 'ok') {
      setPhase('done')
      setMessage(t('wizardAccountReady'))
      onAdded()
      window.setTimeout(finishAndClose, 900)
      return
    }
    setPhase('idle')
    setMessage(outcome === 'timeout' ? t('wizardLoginTimeout') : (loginMessage || t('authFailed')))
  }

  function chooseProvider(next: string) {
    if (busy || !typesReady) return
    setAccountType(next)
    setTab('browser')
    setMessage('')
    setStep('login')
  }

  function switchTab(next: AddAccountTab) {
    if (busy || next === tab) return
    setTab(next)
    setMessage('')
    setAuthUrl('')
  }

  function onFileChange(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => setJson(String(reader.result || ''))
    reader.readAsText(file)
    event.target.value = ''
  }

  // 登录流程共享同一份上下文（建号 / 授权 / PAT / 回调）。
  const flow: WizardFlowContext = {
    activeOption,
    name,
    createdId,
    deviceLogin,
    t,
    setPhase,
    setMessage,
    setAuthUrl,
    onAdded,
    finish: finishAndClose,
  }

  return {
    // ── 视图态 ────────────────────────────────────────────
    step,
    tab,
    phase,
    message,
    authUrl,
    callbackUrl,
    isDone: phase === 'done',
    busy,
    typesLoading,
    typesReady,
    providerOptions,
    activeOption,
    hint,
    settingsLocked: Boolean(createdId.current) || busy,
    showCallbackPaste,
    hasBrowserLogin,
    showPatTab,
    showImportTab,
    name,
    pat,
    json,
    batchRows,
    fileInput,
    // ── 视图回调 ──────────────────────────────────────────
    chooseProvider,
    backToMethod: () => setStep('method'),
    switchTab,
    setName,
    setPat,
    setJson,
    setCallbackUrl,
    onPickFile: () => fileInput.current?.click(),
    onFileChange,
    // ── 动作 ─────────────────────────────────────────────
    startBrowser: () => startBrowserFlow(flow),
    submitCallback: () => submitCallbackFlow(flow, callbackUrl),
    submitPat: () => submitPatFlow(flow, pat),
    submitImportFromJson: () => void submitImport({
      json,
      name,
      activeOption,
      t,
      setPhase,
      setMessage,
      setBatchRows,
      onAdded,
      finish: finishAndClose,
    }),
    close,
  }
}
