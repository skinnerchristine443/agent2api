import { useMemo, useState } from 'react'

import { fetchAccounts, fetchModels, testChat } from '@/api/overview'
import type { ModelInfo } from '@/api/types'
import { useApiQuery } from '@/hooks/useApiQuery'

export type RequestState = 'idle' | 'loading' | 'success' | 'error'

function errorText(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

/**
 * API 调试台的数据层 hook：账号列表（`/api/accounts`）与全池模型（`/api/models`）
 * 各一次查询；钉死账号时按账号拉取目录（`/api/models?account=`），切换账号即换
 * depsKey；真实调用 `POST /api/chat`（非流式），保留原始响应/错误体与耗时。
 *
 * 位置：按四批统一规则放 `src/hooks/`（数据层经 hooks 取数，`pages/**` 不做
 * `@/api` 值导入）；消费方仅 `pages/access/`。
 */
export function useAccessData() {
  const accountsQuery = useApiQuery((signal) => fetchAccounts(false, signal), 'access:accounts')
  const poolModelsQuery = useApiQuery((signal) => fetchModels(undefined, false, undefined, signal), 'access:models')
  const accounts = useMemo(() => accountsQuery.data?.data || [], [accountsQuery.data])
  const poolModels = useMemo(() => poolModelsQuery.data?.data || [], [poolModelsQuery.data])

  const [accountId, setAccountId] = useState('')
  const catalogQuery = useApiQuery(
    (signal) => fetchModels(accountId, false, undefined, signal),
    `access:catalog:${accountId}`,
    { enabled: Boolean(accountId) },
  )
  const catalogLoading = Boolean(accountId) && catalogQuery.loading
  // 取数途中（含切换账号的首帧）不沿用上一账号的目录，与迁移前的 catalogReady 语义一致。
  const models: ModelInfo[] = accountId ? (catalogLoading ? [] : catalogQuery.data?.data || []) : poolModels
  const modelsError = accountId && !catalogLoading ? catalogQuery.error || '' : ''

  const readyAccounts = accounts.filter((account) => account.enabled !== false && (
    account.ready === true || account.hot === true || account.status === 'ready' || account.status === 'hot'
  ))

  const [model, setModel] = useState('')
  const selectedModel = models.some((item) => item.id === model) ? model : models[0]?.id || ''
  const [prompt, setPrompt] = useState('只回复OK')
  const payload = useMemo(() => ({
    model: selectedModel,
    stream: false,
    messages: [{ role: 'user', content: prompt || '只回复OK' }],
  }), [prompt, selectedModel])

  const [output, setOutput] = useState('')
  const [requestState, setRequestState] = useState<RequestState>('idle')
  const [elapsedMs, setElapsedMs] = useState<number | null>(null)

  async function runTest() {
    const startedAt = performance.now()
    setRequestState('loading')
    setElapsedMs(null)
    setOutput('')
    try {
      const data = await testChat(payload.model, payload.messages[0].content, accountId || undefined)
      setOutput(JSON.stringify(data, null, 2))
      setRequestState('success')
    } catch (error) {
      setOutput(errorText(error))
      setRequestState('error')
    } finally {
      setElapsedMs(Math.round(performance.now() - startedAt))
    }
  }

  return {
    accounts,
    readyAccounts,
    accountId,
    setAccountId,
    models,
    modelsLoading: catalogLoading,
    modelsError,
    model,
    setModel,
    selectedModel,
    prompt,
    setPrompt,
    payload,
    output,
    requestState,
    elapsedMs,
    runTest,
  }
}

export type AccessData = ReturnType<typeof useAccessData>
