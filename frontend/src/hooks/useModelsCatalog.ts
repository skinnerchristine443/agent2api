import { useEffect, useMemo, useState } from 'react'

import {
  fetchModelsCached,
  fetchProviders,
  refreshModels,
  updateProviderMaxMode,
  updateProviderReasoning,
} from '@/api/overview'
import type { ModelInfo } from '@/api/types'
import { useI18n } from '@/hooks/I18nContext'
import { useApiQuery } from '@/hooks/useApiQuery'
import { useAsyncAction } from '@/hooks/useAsyncAction'

import { modelProvider, modelSettingsKey, reasoningLabel } from '@/pages/access/modelMeta'
import { providerRegionOptions } from '@/pages/access/modelsFilter'

function errorText(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

/**
 * 模型目录的数据层 hook：模型清单（含区域视图）、渠道描述符、刷新动作，
 * 以及两个 PATCH（更大上下文 / 推理强度）的就地更新与结果提示。
 *
 * 位置：按四批统一规则放 `src/hooks/`（数据层经 hooks 取数，`pages/**` 不
 * 做 `@/api` 值导入）；消费方仅 `pages/access/`。纯函数 helper 留在
 * `pages/access/`（modelMeta / modelsFilter），本文件反向引用。
 *
 * 与迁移前 ProvidersPage 的语义一致：目录取数失败静默（保留空目录走空态），
 * 刷新与 PATCH 的结果以 message（成功）/ messageError（失败）供页面提示。
 */
export function useModelsCatalog() {
  const { t } = useI18n()
  // 目录数据源保持既有口径：区域视图（view=regional）+ 30s 内存缓存。
  const modelsQuery = useApiQuery((signal) => fetchModelsCached(undefined, 'regional', signal), 'models:regional')
  const providersQuery = useApiQuery((signal) => fetchProviders(signal), 'models:providers')

  // 本地副本：PATCH 成功后就地改写（开关 / 档位 / 窗口），新一次取数到达时整体覆盖。
  const [models, setModels] = useState<ModelInfo[]>([])
  useEffect(() => {
    if (modelsQuery.data) setModels(modelsQuery.data.data || []) // eslint-disable-line react/set-state-in-effect -- 目录取数到达即镜像到本地副本
  }, [modelsQuery.data])

  const providerOptions = useMemo(
    () => providerRegionOptions(providersQuery.data?.data || []),
    [providersQuery.data],
  )

  const [savingKey, setSavingKey] = useState('')
  const [message, setMessage] = useState('')
  const [messageError, setMessageError] = useState(false)

  const refreshAction = useAsyncAction(async () => {
    setMessage('')
    setMessageError(false)
    try {
      const data = await refreshModels(undefined, 'regional')
      setModels(data.data || [])
    } catch (error) {
      setMessageError(true)
      setMessage(errorText(error))
    }
  })

  function applyMaxMode(model: ModelInfo, maxMode: boolean) {
    const key = modelSettingsKey(model)
    const provider = modelProvider(model)
    setModels((prev) => prev.map((item) => {
      if (modelSettingsKey(item) !== key || modelProvider(item) !== provider) return item
      const effort = item.reasoning_effort || item.reasoning_default || ''
      const itemDev = item.catalog_context_length || item.default_context_length || item.context_length || 0
      const itemMax = item.catalog_context_length_max || 0
      return {
        ...item,
        max_mode: maxMode,
        // 这里只改动开关及其窗口。prompt/output 上限保持为 catalog 默认值；
        // 各视图在渲染时从 *_max 选取 Max 档位，因此关闭 max mode 会恢复默认。
        context_length: maxMode && itemMax ? itemMax : itemDev,
        default_context_length: itemDev,
        context_custom: maxMode || Boolean(effort && effort !== item.reasoning_default),
      }
    }))
  }

  function applyReasoning(model: ModelInfo, effort: string) {
    const key = modelSettingsKey(model)
    const provider = modelProvider(model)
    setModels((prev) => prev.map((item) => {
      if (modelSettingsKey(item) !== key || modelProvider(item) !== provider) return item
      return {
        ...item,
        reasoning_effort: effort,
        context_custom: Boolean(item.max_mode) || Boolean(effort && effort !== item.reasoning_default),
      }
    }))
  }

  async function onToggleMaxMode(model: ModelInfo, maxMode: boolean) {
    if (!model.supports_max_mode) {
      setMessageError(true)
      setMessage(t('contextMaxUnavailable'))
      return
    }
    const key = modelSettingsKey(model)
    const provider = modelProvider(model)
    setSavingKey(key)
    setMessage('')
    setMessageError(false)
    try {
      await updateProviderMaxMode(provider, key, maxMode)
      applyMaxMode(model, maxMode)
      setMessage(maxMode ? t('contextMaxOn', { model: model.id }) : t('contextMaxOff', { model: model.id }))
    } catch (error) {
      setMessageError(true)
      setMessage(errorText(error))
    } finally {
      setSavingKey('')
    }
  }

  async function onReasoningChange(model: ModelInfo, effort: string) {
    const provider = modelProvider(model)
    if (provider !== 'trae' && provider !== 'workbuddy') return
    const key = modelSettingsKey(model)
    setSavingKey(key)
    setMessage('')
    setMessageError(false)
    try {
      await updateProviderReasoning(provider, key, effort)
      applyReasoning(model, effort)
      setMessage(t('reasoningSaved', { model: model.id, level: reasoningLabel(t, effort) }))
    } catch (error) {
      setMessageError(true)
      setMessage(errorText(error))
    } finally {
      setSavingKey('')
    }
  }

  return {
    models,
    providerOptions,
    /** 目录首取在途（整页骨架用）。 */
    loading: modelsQuery.loading,
    /** 刷新在途（表体骨架 / 刷新按钮 pending）。 */
    refreshing: refreshAction.pending,
    savingKey,
    message,
    messageError,
    refresh: refreshAction.run,
    onToggleMaxMode,
    onReasoningChange,
  }
}

export type ModelsCatalog = ReturnType<typeof useModelsCatalog>
