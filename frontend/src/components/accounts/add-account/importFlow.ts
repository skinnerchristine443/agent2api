import {
  importAccount,
  importAccountsBatch,
  type ImportBatchItem,
} from '@/api/overview'
import type { Translate } from '@/i18n/messages'

import type { ProviderOption } from './providerOptions'
import type { AddAccountPhase } from './types'

export type ImportFlowContext = {
  json: string
  name: string
  activeOption: ProviderOption | undefined
  t: Translate
  setPhase: (phase: AddAccountPhase) => void
  setMessage: (message: string) => void
  setBatchRows: (rows: ImportBatchItem[]) => void
  onAdded: () => void
  finish: () => void
}

/**
 * 凭证导入（单个包 / JSON 数组批量）：
 * - 解析失败 / 形状不符 → 就地提示，不进入 busy；
 * - 批量：服务端逐项报告（imported / skipped / error），模态保持打开供阅读，
 *   出错不丢弃整批运行；仅在确有成功项时通知列表刷新。
 */
export async function submitImport(ctx: ImportFlowContext): Promise<void> {
  const { t, activeOption, setPhase, setMessage } = ctx
  setMessage('')
  let bundle: any
  try {
    bundle = JSON.parse(ctx.json)
  } catch {
    setMessage(t('wizardBadJson'))
    return
  }
  if (!bundle || typeof bundle !== 'object') {
    setMessage(t('wizardBadJson'))
    return
  }
  if (!activeOption) {
    setMessage(t('accountTypeHint'))
    return
  }
  // JSON 数组是轻量级批量形式：一次粘贴，逐项报告。
  if (Array.isArray(bundle)) {
    await submitBatchImport(ctx, bundle)
    return
  }
  if (!bundle.format) {
    if (activeOption.descriptor.id === 'workbuddy') bundle.format = 'workbuddy-oauth-v1'
    else if (activeOption.descriptor.id === 'trae') bundle.format = 'trae-oauth-v1'
  }
  try {
    setPhase('busy')
    // 运行参数由后端取「渠道 × 区域」默认物化，不再随导入下发。
    await importAccount({
      ...bundle,
      name: ctx.name.trim() || bundle.name,
      enabled: true,
      provider: activeOption.provider,
      region: activeOption.region,
    })
    setPhase('done')
    setMessage(t('accountImported'))
    ctx.onAdded()
    window.setTimeout(ctx.finish, 700)
  } catch (error) {
    setPhase('idle')
    setMessage(error instanceof Error ? error.message : String(error))
  }
}

async function submitBatchImport(ctx: ImportFlowContext, items: unknown[]) {
  const { t, activeOption, setPhase, setMessage, setBatchRows } = ctx
  if (!activeOption) {
    setMessage(t('accountTypeHint'))
    return
  }
  if (items.length === 0) {
    setMessage(t('wizardBadJson'))
    return
  }
  const enriched = items.map((item) =>
    item && typeof item === 'object'
      ? {
          ...(item as Record<string, unknown>),
          enabled: true,
          provider: activeOption.provider,
          region: activeOption.region,
        }
      : item,
  )
  try {
    setPhase('busy')
    setBatchRows([])
    const output = await importAccountsBatch(enriched)
    setPhase('idle')
    setBatchRows(output?.results || [])
    setMessage(
      t('importBatchSummary', {
        imported: String(output?.imported || 0),
        skipped: String(output?.skipped || 0),
        errors: String(output?.errors || 0),
      }),
    )
    if ((output?.imported || 0) > 0) ctx.onAdded()
  } catch (error) {
    setPhase('idle')
    setMessage(error instanceof Error ? error.message : String(error))
  }
}
