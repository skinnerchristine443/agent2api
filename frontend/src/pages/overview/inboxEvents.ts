import type { QuotaAlert } from '@/api/overview'
import type { Translate } from '@/i18n/messages'
import { accountState, cooldownLabel, formatQuotaAmount, type AccountRow } from '@/lib/account'
import { accountChannelKey, accountProviderLabel } from '@/lib/provider'

/**
 * 待办收件箱 · 五类事件的聚合（设计 D16 / 主方案 §3.1 · M1）。
 *
 * 纯函数：账号快照 + 额度告警 + 翻译函数 → 已排序的收件箱事件列表。
 * 事件数据全部从既有接口派生（零后端新增）：
 *   ① 限额告警   ← `/api/alerts`（quota_exceeded / quota_low，后端权威派生）
 *   ② 登录失效   ← accounts 运行态（auth_failed / login_required / dead）
 *   ③ 签到失败   ← accounts `last_checkin_status === 'error'`
 *   ④ 冷却       ← accounts 冷却态（down_until）
 *   ⑤ 额度将到期 ← accounts `quota.expires_at`（≤7 天窗口，≤3 天升级为 danger）
 *
 * 排序：严重度（danger → warning）→ 事件时间倒序（无时间的置后）→ id 字典序
 * （末位比较保证输出确定性，便于测试与 React key 稳定）。
 */

export type InboxKind = 'quota' | 'authFailed' | 'checkinFailed' | 'cooling' | 'expiry'

export type InboxSeverity = 'danger' | 'warning'

export type InboxEvent = {
  /** 稳定标识（React key / 测试锚点）。 */
  id: string
  kind: InboxKind
  severity: InboxSeverity
  /** 类别名（已翻译，如「限额告警」）。 */
  title: string
  /** 渠道标签（已翻译，如「WorkBuddy · 国际版」；账号缺失时为 ''）。 */
  providerLabel: string
  accountId: string
  accountName: string
  /** 描述（已翻译）。 */
  detail: string
  /** 右列时间/倒计时标签（'' = 不显示）。 */
  meta: string
  /** 跳转处置路径（带 provider 渠道页签对齐 + focus；接收端负责高亮）。 */
  to: string
  /** 事件时间戳（排序用，毫秒；缺失 = 0）。 */
  at: number
}

/** 额度到期展示窗口（天）：进入窗口即入收件箱。 */
export const EXPIRY_WINDOW_DAYS = 7
/** 额度到期升级线（天）：≤3 天按 danger 呈现。 */
export const EXPIRY_DANGER_DAYS = 3

const DAY_MS = 24 * 60 * 60 * 1000

function timestampOf(value?: string | null): number {
  if (!value) return 0
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : 0
}

/** HH:mm（本地时区；解析失败返回 ''）。 */
function hhmm(value?: string | null): string {
  const at = timestampOf(value)
  if (!at) return ''
  const date = new Date(at)
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}

function encodeFocus(id: string) {
  return encodeURIComponent(id)
}

/** 渠道键（批次 15：`<provider>-<region>` 组合；账号缺失时返回空串——不猜渠道）。 */
function channelOf(account?: AccountRow) {
  return account ? accountChannelKey(account.provider, account.region) : ''
}

/** 账号 → 需关注行动视图 + 高亮目标行（带渠道键，页签对齐）。 */
function attentionUrl(id: string, channel: string) {
  const param = channel ? `&provider=${encodeURIComponent(channel)}` : ''
  return `/accounts?quick=attn${param}&focus=${encodeFocus(id)}`
}

/** 账号 → 在途（冷却）视图 + 高亮目标行（带渠道键）。 */
function transitUrl(id: string, channel: string) {
  const param = channel ? `&provider=${encodeURIComponent(channel)}` : ''
  return `/accounts?quick=transit${param}&focus=${encodeFocus(id)}`
}

/** 账号 → 账号池（目标渠道）+ 高亮目标行（行内签到列可直接处置）。 */
function checkinUrl(id: string, channel: string) {
  const param = channel ? `provider=${encodeURIComponent(channel)}&` : ''
  return `/accounts?${param}focus=${encodeFocus(id)}`
}

/** 账号 → 额度页 + 定位账号。 */
function expiryUrl(id: string) {
  return `/quota?account=${encodeFocus(id)}`
}

export type InboxInput = {
  accounts: AccountRow[]
  alerts: QuotaAlert[]
  t: Translate
  /** 注入时钟（测试用；默认 Date.now()）。 */
  now?: number
}

export function buildInboxEvents({ accounts, alerts, t, now = Date.now() }: InboxInput): InboxEvent[] {
  const events: InboxEvent[] = []
  const accountById = new Map(accounts.map((account) => [account.id, account]))
  const nameOf = (account: AccountRow) => account.name || account.id

  // ① 限额告警（后端派生，作权威源；quota_low 仅在账号设有预留底线时存在）
  for (const alert of alerts) {
    const account = accountById.get(alert.account_id)
    const exceeded = alert.category === 'quota_exceeded'
    const accountName = alert.account_name || (account ? nameOf(account) : alert.account_id)
    events.push({
      id: `quota:${alert.account_id}:${alert.category}`,
      kind: 'quota',
      severity: exceeded ? 'danger' : 'warning',
      title: t('inboxKindQuota'),
      providerLabel: account ? accountProviderLabel(account.provider, account.region, t) : '',
      accountId: alert.account_id,
      accountName,
      detail: exceeded
        ? t('inboxQuotaExceeded', { name: accountName })
        : t('inboxQuotaLow', { name: accountName }),
      meta: '',
      to: attentionUrl(alert.account_id, channelOf(account)),
      at: 0,
    })
  }

  for (const account of accounts) {
    const name = nameOf(account)
    const providerLabel = accountProviderLabel(account.provider, account.region, t)
    const state = accountState(account)

    // ② 登录失效（认证失败 / 需重新登录 / 死号——都须重新认证或人工处置）
    if (state === 'auth_failed' || state === 'login' || state === 'dead') {
      events.push({
        id: `auth:${account.id}`,
        kind: 'authFailed',
        severity: 'danger',
        title: t('inboxKindAuthFailed'),
        providerLabel,
        accountId: account.id,
        accountName: name,
        detail: t('inboxAuthFailed', { name }),
        meta: '',
        to: attentionUrl(account.id, channelOf(account)),
        at: 0,
      })
    }

    // ③ 签到失败
    if (account.last_checkin_status === 'error') {
      const message = account.last_checkin_msg ? `（${account.last_checkin_msg}）` : ''
      events.push({
        id: `checkin:${account.id}`,
        kind: 'checkinFailed',
        severity: 'danger',
        title: t('inboxKindCheckinFailed'),
        providerLabel,
        accountId: account.id,
        accountName: name,
        detail: t('inboxCheckinFailed', { name, msg: message }),
        meta: hhmm(account.last_checkin_at),
        to: checkinUrl(account.id, channelOf(account)),
        at: timestampOf(account.last_checkin_at),
      })
    }

    // ④ 冷却中
    if (state === 'cooling') {
      events.push({
        id: `cooling:${account.id}`,
        kind: 'cooling',
        severity: 'warning',
        title: t('inboxKindCooling'),
        providerLabel,
        accountId: account.id,
        accountName: name,
        detail: t('inboxCooling', { name }),
        meta: cooldownLabel(account.down_until || account.cooldown_until, now),
        to: transitUrl(account.id, channelOf(account)),
        at: 0,
      })
    }

    // ⑤ 额度将到期（≤7 天窗口；≤3 天升级 danger）
    const quota = account.quota
    if (quota?.expires_at && quota.expires_at > 0 && (quota.expiring_remain ?? 0) > 0) {
      const days = Math.ceil((quota.expires_at * 1000 - now) / DAY_MS)
      if (days >= 0 && days <= EXPIRY_WINDOW_DAYS) {
        const amount = `${formatQuotaAmount(quota.expiring_remain)} ${quota.unit || 'credits'}`
        events.push({
          id: `expiry:${account.id}`,
          kind: 'expiry',
          severity: days <= EXPIRY_DANGER_DAYS ? 'danger' : 'warning',
          title: t('inboxKindExpiry'),
          providerLabel,
          accountId: account.id,
          accountName: name,
          detail: t('inboxExpiry', { name, amount, days }),
          meta: t('inboxDaysLeft', { days }),
          to: expiryUrl(account.id),
          at: quota.expires_at * 1000,
        })
      }
    }
  }

  const severityRank: Record<InboxSeverity, number> = { danger: 0, warning: 1 }
  events.sort((left, right) => (
    severityRank[left.severity] - severityRank[right.severity]
    || right.at - left.at
    || left.id.localeCompare(right.id)
  ))
  return events
}
