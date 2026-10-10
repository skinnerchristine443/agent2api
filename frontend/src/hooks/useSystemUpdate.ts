import { useCallback, useEffect, useMemo, useState } from 'react'
import { applyPreparedSystemUpdate, cancelSystemUpdate, fetchSystemUpdate, rollbackSystemUpdate, startSystemUpdate, type StartUpdateResult, type SystemUpdateInfo } from '@/api/system'
import { useApiQuery } from '@/hooks/useApiQuery'
import { useNowTick } from '@/hooks/useNowTick'
import { updateStateKey, type Translate } from '@/i18n/messages'
import { buildVersionHistory } from '@/lib/versionHistory'
import { updatePollActive } from './updatePolling'

const busyStates = new Set(['preparing', 'preparing_image', 'checking', 'backing_up', 'submitting', 'running', 'queued', 'pulling', 'host_binary', 'image_ready', 'recreating', 'rolling_back'])
const applyJobStates = new Set(['backing_up', 'running'])
const applyAgentStates = new Set(['recreating', 'rolling_back'])
const progressAgentStates = new Set(['queued', 'pulling', 'host_binary', 'image_ready', 'preparing', 'recreating', 'checking', 'rolling_back'])

/**
 * 更新流程状态机（原 SystemPage 的更新区逻辑原样搬移，仅轮询 hooks 化）。
 *
 * - 2s 状态轮询走 useApiQuery 条件轮询（进行中 / 刚更新才轮询，完成即停）；
 * - 1s 计时走 useNowTick(active: busy)（共享单例；原每实例 setInterval）；
 * - 更新完成的 1s 倒计时保留原 setTimeout 链逻辑；
 * - 错误写入共享的页面 error（经 setError 注入），静默轮询不清理既有错误。
 */
export function useSystemUpdate(t: Translate, setError: (message: string) => void) {
  const [info, setInfo] = useState<SystemUpdateInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [checking, setChecking] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [started, setStarted] = useState<StartUpdateResult | null>(null)
  const [reloadIn, setReloadIn] = useState<number | null>(null)
  const [initialVersion, setInitialVersion] = useState('')
  const [restoreTarget, setRestoreTarget] = useState('')

  const load = useCallback(async (force = false, quiet = false) => {
    if (force && !quiet) setChecking(true)
    try {
      const result = await fetchSystemUpdate(force)
      setInfo(result)
      setInitialVersion((current) => current || result.current_version || '')
      const failed = result.update?.state === 'failed' || result.update?.state === 'rolled_back' || result.agent?.state === 'failed' || result.agent?.state === 'rolled_back'
      if (failed) {
        setError(result.update?.error || result.agent?.error || t('updateFailedHint'))
      } else if (!quiet) {
        setError('')
      }
    } catch (err) {
      if (!quiet) setError(err instanceof Error ? err.message : t('updateCheckFailedHint'))
    } finally {
      setLoading(false)
      if (force && !quiet) setChecking(false)
    }
  }, [t, setError])

  const agentState = info?.agent?.state || 'unavailable'
  const preparationState = info?.update?.state || ''
  const applying = applyJobStates.has(preparationState) || applyAgentStates.has(agentState) || Boolean(info?.update?.backup_path && busyStates.has(preparationState))
  const readyToApply = !applying && (preparationState === 'ready_to_apply' || agentState === 'ready_to_apply')
  const waitingForJob = Boolean(started?.job_id) && info?.update?.job_id !== started?.job_id && !readyToApply && !applying
  const preparing = !applying && !readyToApply && (busyStates.has(preparationState) || busyStates.has(agentState) || waitingForJob)
  const busy = preparing || applying
  const succeeded = preparationState === 'succeeded' || agentState === 'succeeded'
  const justUpdated = Boolean(succeeded && initialVersion && info?.current_version && info.current_version !== initialVersion)

  // 1s 计时（仅进行中有意义）：共享单例时钟，busy=false 即停。
  const now = useNowTick(busy)

  const targetVersion = info?.update?.target_version || info?.agent?.target_version || ''
  const newerThanPrepared = Boolean(readyToApply && info?.next_version && targetVersion && info.next_version !== targetVersion)
  const startedAt = info?.update?.started_at || info?.agent?.started_at
  const elapsed = startedAt && busy ? Math.max(0, Math.round((now - Date.parse(startedAt)) / 1000)) : 0
  const visibleState = justUpdated
    ? 'succeeded'
    : readyToApply
      ? 'ready_to_apply'
      : busy && progressAgentStates.has(agentState)
        ? agentState
        : (preparationState || agentState)
  // state code 来自后端状态机，因此标签通过带类型的 key map
  // 解析：未知 code 直接显示原始 code，
  // 而不是拼出一个已不存在的 key。
  const updateStateKeyName = visibleState ? updateStateKey(visibleState) : undefined
  const updateStateText = updateStateKeyName ? t(updateStateKeyName) : visibleState

  // 更新完成 → 启动自动刷新倒计时（原 render 期 setState 改为提交后 effect）。
  useEffect(() => {
    if (justUpdated && reloadIn == null) setReloadIn(3) // eslint-disable-line react/set-state-in-effect -- 更新完成即启动刷新倒计时
  }, [justUpdated, reloadIn])

  // 2s 更新状态轮询：进行中 / 刚更新才轮询，完成即停（条件由 updatePollActive 持有）。
  const pollActive = updatePollActive({ busy, justUpdated, reloadIn })
  const updatePoll = useApiQuery(
    (signal) => fetchSystemUpdate(false, signal),
    'system:update',
    { pollMs: pollActive ? 2000 : null, enabled: pollActive },
  )
  // 轮询结果按原 quiet 语义合并：只更新数据与失败提示，不清理已有错误。
  useEffect(() => {
    const result = updatePoll.data
    if (!result) return
    setInfo(result) // eslint-disable-line react/set-state-in-effect -- 轮询结果到达即合并进本地状态
    setInitialVersion((current) => current || result.current_version || '')
    const failed = result.update?.state === 'failed' || result.update?.state === 'rolled_back' || result.agent?.state === 'failed' || result.agent?.state === 'rolled_back'
    if (failed) setError(result.update?.error || result.agent?.error || t('updateFailedHint'))
  }, [updatePoll.data, t, setError])

  useEffect(() => {
    if (reloadIn == null) return
    if (reloadIn <= 0) {
      window.location.reload()
      return
    }
    const timer = window.setTimeout(() => setReloadIn((current) => (current == null ? current : current - 1)), 1000)
    return () => window.clearTimeout(timer)
  }, [reloadIn])

  const canPrepare = Boolean(info?.managed && info?.has_update && info?.next_version && info?.agent?.available && !readyToApply && !busy && !submitting && reloadIn == null)
  const canApply = Boolean(readyToApply && info?.agent?.available && !applying && !submitting && reloadIn == null)
  const canCancel = Boolean(info?.agent?.available && (preparing || readyToApply) && !applying && !submitting && reloadIn == null)
  const canRollback = Boolean(info?.managed && info?.agent?.available && (info.rollback_versions?.length ?? 0) > 0 && !readyToApply && !busy && !submitting && reloadIn == null)
  const historyReleases = useMemo(
    () => buildVersionHistory(info?.current_version || '', info?.next_version, info?.release, info?.recent_releases, info?.rollback_versions),
    [info?.current_version, info?.next_version, info?.release, info?.recent_releases, info?.rollback_versions],
  )
  const rollbackTags = useMemo(() => (info?.rollback_versions || []).map((release) => release.tag_name), [info?.rollback_versions])
  const statusHint = reloadIn != null
    ? t('updateReloadingHint')
    : readyToApply
      ? t('updateReadyTargetHint', { version: targetVersion || info?.next_version || '' })
      : applying
        ? t('updateApplyingHint')
        : t('updatePreparingImageHint')

  const prepareUpdate = useCallback(async () => {
    setSubmitting(true)
    setStarted(null)
    setError('')
    try {
      const result = await startSystemUpdate()
      setStarted(result)
      await load(false, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('updateFailedHint'))
      await load(false, true)
    } finally {
      setSubmitting(false)
    }
  }, [load, setError, t])

  const confirmUpdate = useCallback(async () => {
    setSubmitting(true)
    setError('')
    try {
      const result = await applyPreparedSystemUpdate()
      setStarted(result)
      await load(false, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('updateFailedHint'))
      await load(false, true)
    } finally {
      setSubmitting(false)
    }
  }, [load, setError, t])

  const rollbackTo = useCallback(async (version: string) => {
    setSubmitting(true)
    setStarted(null)
    setError('')
    try {
      const result = await rollbackSystemUpdate(version)
      setStarted(result)
      await load(false, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('updateFailedHint'))
      await load(false, true)
    } finally {
      setSubmitting(false)
    }
  }, [load, setError, t])

  const cancelPreparedUpdate = useCallback(async () => {
    setSubmitting(true)
    setError('')
    try {
      await cancelSystemUpdate()
      setStarted(null)
      await load(true, true)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('updateFailedHint'))
      await load(false, true)
    } finally {
      setSubmitting(false)
    }
  }, [load, setError, t])

  return {
    info,
    loading,
    checking,
    submitting,
    started,
    reloadIn,
    initialVersion,
    restoreTarget,
    setRestoreTarget,
    applying,
    readyToApply,
    preparing,
    busy,
    justUpdated,
    targetVersion,
    newerThanPrepared,
    elapsed,
    updateStateText,
    canPrepare,
    canApply,
    canCancel,
    canRollback,
    historyReleases,
    rollbackTags,
    statusHint,
    load,
    prepareUpdate,
    confirmUpdate,
    cancelPreparedUpdate,
    rollbackTo,
  }
}

export type SystemUpdateFlow = ReturnType<typeof useSystemUpdate>
