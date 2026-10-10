import { completeLoginCallback, createAccount, loginWithPat, startDeviceLogin } from '@/api/overview'
import type { DeviceLoginPoll } from '@/components/accounts/useDeviceLoginPoll'
import type { Translate } from '@/i18n/messages'

import type { ProviderOption } from './providerOptions'
import type { AddAccountPhase } from './types'

export type WizardFlowContext = {
  activeOption: ProviderOption | undefined
  name: string
  /** 首次创建的账号 id（同一次向导复用，避免重复建号）。 */
  createdId: { current: string }
  deviceLogin: DeviceLoginPoll
  t: Translate
  setPhase: (phase: AddAccountPhase) => void
  setMessage: (message: string) => void
  setAuthUrl: (url: string) => void
  onAdded: () => void
  finish: () => void
}

/** 惰性建号：向导内多次尝试（浏览器 / PAT / 回调）复用同一个隔离运行时。 */
export async function ensureAccount(ctx: WizardFlowContext): Promise<string> {
  if (ctx.createdId.current) return ctx.createdId.current
  const option = ctx.activeOption
  if (!option) throw new Error(ctx.t('accountTypeHint'))
  // 运行参数（最大并发 / 代理 / 丢弃系统提示词 / 日防护）不再随建号下发，
  // 由后端取「渠道 × 区域」默认物化（见 internal/accounts/account_defaults.go）。
  const account = await createAccount(ctx.name.trim() || ctx.t('account'), option.provider, option.region)
  const id = account?.id || account?.data?.id
  if (!id) throw new Error('create account returned no id')
  ctx.createdId.current = id
  return id
}

function failureText(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

/** 浏览器（设备码）登录：建号 → 启动会话 → 打开授权页 → 交给轮询等待授权。 */
export function startBrowserFlow(ctx: WizardFlowContext) {
  ctx.setMessage('')
  ctx.setAuthUrl('')
  void (async () => {
    try {
      ctx.setPhase('busy')
      const id = await ensureAccount(ctx)
      ctx.setMessage(ctx.t('wizardStartingSession'))
      const output = await startDeviceLogin(id)
      if (output.authUrl) {
        ctx.setAuthUrl(output.authUrl)
        window.open(output.authUrl, '_blank', 'noopener,noreferrer')
      }
      ctx.setMessage(ctx.t('wizardWaitingBrowser'))
      ctx.setPhase('polling')
      ctx.deviceLogin.start(id)
    } catch (error) {
      ctx.setPhase('idle')
      ctx.setMessage(failureText(error))
    }
  })()
}

/** Trae 回调地址粘贴提交（浏览器停在 127.0.0.1 时的兜底路径）。 */
export function submitCallbackFlow(ctx: WizardFlowContext, callbackUrl: string) {
  const pasted = callbackUrl.trim()
  if (!pasted) {
    ctx.setMessage(ctx.t('wizardCallbackPh'))
    return
  }
  void (async () => {
    try {
      ctx.setPhase('busy')
      const id = await ensureAccount(ctx)
      ctx.deviceLogin.cancel()
      await completeLoginCallback(id, pasted)
      ctx.setPhase('done')
      ctx.setMessage(ctx.t('wizardAccountReady'))
      ctx.onAdded()
      window.setTimeout(ctx.finish, 900)
    } catch (error) {
      ctx.setPhase('polling')
      ctx.setMessage(failureText(error))
    }
  })()
}

/** PAT 登录：建号 → 用个人访问令牌登录。 */
export function submitPatFlow(ctx: WizardFlowContext, pat: string) {
  const token = pat.trim()
  if (!token) {
    ctx.setMessage(ctx.t('pastePatFirst'))
    return
  }
  ctx.setMessage('')
  void (async () => {
    try {
      ctx.setPhase('busy')
      const id = await ensureAccount(ctx)
      ctx.setMessage(ctx.t('wizardStartingSession'))
      await loginWithPat(token, id)
      ctx.setPhase('done')
      ctx.setMessage(ctx.t('patDone'))
      ctx.onAdded()
      window.setTimeout(ctx.finish, 700)
    } catch (error) {
      ctx.setPhase('idle')
      ctx.setMessage(failureText(error))
    }
  })()
}
