import type { Translate } from '@/i18n/messages'
import { useEffect, useRef } from 'react'
import { Button, Dropdown, NumberField, Tooltip } from '@heroui/react'
import {
  ArrowClockwise,
  CalendarCheck,
  Copy,
  Cube,
  DotsThreeVertical,
  Key,
  ListBullets,
  PencilSimple,
  TrashSimple,
  WarningCircle,
} from '@phosphor-icons/react'
import { ProviderMark } from '@/components/brand/ProviderMark'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import {
  accountState,
  cooldownLabel,
  formatQuotaAmount,
  modelCooldownEntries,
  quotaDisplay,
  type AccountRow,
} from '@/lib/account'
import { accountProviderLabel } from '@/lib/provider'
import { useNowTick } from '@/hooks/useNowTick'

/** 账号行操作态（页面组装与行组件共享；重构批次 1b 自旧卡片迁出）。 */
export type AccountBusyKind =
  | 'create'
  | 'import'
  | 'device'
  | 'pat'
  | 'callback'
  | 'rewarm'
  | 'refresh'
  | 'toggle'
  | 'delete'
  | 'export'
  | 'settings'
  | 'checkin'
  | 'cooldowns'

type Props = {
  account: AccountRow
  busyKind: AccountBusyKind | ''
  /** 收件箱跳转的目标行：焦点高亮 + 首次滚动到位。 */
  focus?: boolean
  t: Translate
  onExport: () => void
  onRefresh?: () => void
  onDelete: () => void
  onToggle: (selected: boolean) => void
  onToggleModelRequests?: (selected: boolean) => void
  onToggleAutoCheckin?: (selected: boolean) => void
  onCheckin?: () => void
  onViewCheckins?: () => void
  onClearCooldowns?: () => void
  /** ⋯ 菜单：重命名（编辑弹窗移除后唯一的改名入口）。 */
  onRename?: () => void
  /** 认证方式列：点击打开认证模态（设备登录 / PAT / 回调）。 */
  onToggleAuthPanel?: () => void
  onPriorityChange?: (priority: number) => void
  onViewModels: () => void
}

function stateCopyFor(state: ReturnType<typeof accountState>, cooldown: string, t: Translate) {
  if (state === 'hot') return t('signedIn')
  if (state === 'ready') return t('ready')
  if (state === 'cooling') return cooldown ? `${t('cooling')} ${cooldown}` : t('cooling')
  if (state === 'quota_exhausted') return t('quotaExceeded')
  if (state === 'loading') return t('quotaLoading')
  if (state === 'starting') return t('starting')
  if (state === 'unavailable') return t('quotaUnavailable')
  if (state === 'dead') return cooldown ? `${t('dead')} ${cooldown}` : t('dead')
  if (state === 'auth_failed') return t('authFailed')
  if (state === 'disabled') return t('disabled')
  return t('needLogin')
}

function stateTone(state: ReturnType<typeof accountState>): 'ok' | 'warn' | 'danger' | 'muted' {
  if (state === 'hot' || state === 'ready') return 'ok'
  if (state === 'cooling') return 'warn'
  if (state === 'quota_exhausted' || state === 'login' || state === 'unavailable' || state === 'dead' || state === 'auth_failed') return 'danger'
  if (state === 'disabled') return 'muted'
  return 'muted'
}

/** 认证方式列文案：账号未认证时提示需要认证，否则展示认证方式本身。 */
function authLabelFor(account: AccountRow, t: Translate): string {
  const kind = String(account.auth_type || 'none').toLowerCase()
  if (kind === 'none' || kind === '') return t('needLogin')
  if (kind === 'pat') return t('pat')
  if (kind === 'oauth' || kind === 'device') return t('oauthDeviceFlow')
  return kind
}

/**
 * 账号池行：单行承载全部信息与操作，**不再展开**（设计决策 2026-10-10）。
 *
 * 行内列：标记 · 账号 · 运行状态 · 认证方式 · 优先级 · 接收模型请求 ·
 * 自动签到 · 签到结果 · 立即签到 · 额度 · 最近错误 · 刷新 · ⋯ · 启用状态。
 * 运行参数（最大并发 / 代理 / 丢弃系统提示词 / 四道日限额）已迁到设置，不再逐账号展示。
 */
export function AccountRowItem({
  account,
  busyKind,
  focus,
  t,
  onExport,
  onRefresh,
  onDelete,
  onToggle,
  onToggleModelRequests,
  onToggleAutoCheckin,
  onCheckin,
  onViewCheckins,
  onClearCooldowns,
  onRename,
  onToggleAuthPanel,
  onPriorityChange,
  onViewModels,
}: Props) {
  const state = accountState(account)
  const cooldown = cooldownLabel(account.down_until || account.cooldown_until)
  const restartIn = cooldownLabel(account.next_restart_at)
  const modelCooldowns = modelCooldownEntries(account)
  const quota = quotaDisplay(account)
  const rootRef = useRef<HTMLDivElement>(null)

  // 收件箱跳转：目标行首次挂载后滚动到视口中部（延时一帧等分组布局落定）。
  useEffect(() => {
    if (!focus) return
    const timer = window.setTimeout(() => {
      rootRef.current?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }, 120)
    return () => window.clearTimeout(timer)
  }, [focus])

  // 相对时间展示（冷却 / 重启倒计时）由共享 UI 时钟驱动。
  useNowTick(Boolean(cooldown || restartIn || modelCooldowns.length))
  const stateCopy = stateCopyFor(state, state === 'dead' ? restartIn : cooldown, t)
  const tone = stateTone(state)
  const lastError = account.last_error || account.lastError
  const errorKind = account.last_error_kind || account.kind
  const provider = accountProviderLabel(account.provider, account.region, t)
  const checkinStatus = account.last_checkin_status
  // 行内额度：绝对剩余（tooltip 带单位）。
  const quotaRemainValue = account.quota?.remaining
  const quotaUnit = account.quota?.unit || ''
  const quotaAmount = quotaRemainValue != null && Number.isFinite(quotaRemainValue) ? formatQuotaAmount(quotaRemainValue) : ''
  const quotaAmountTitle = quotaAmount ? `${t('quotaRemaining')} ${quotaAmount}${quotaUnit ? ` ${quotaUnit}` : ''}` : ''
  const priority = Number.isFinite(account.priority) ? Number(account.priority) : 50
  // 行内签到列：短标签 + 状态点色。
  const rowCheckinLabel = checkinStatus === 'success' ? 'checkinRecordSuccess' : checkinStatus === 'already' ? 'checkinRecordAlready' : checkinStatus === 'skipped' ? 'checkinRecordSkipped' : checkinStatus === 'error' ? 'checkinRecordFailed' : 'acctCheckinNone'
  const checkinTone = checkinStatus === 'success' || checkinStatus === 'already' ? 'ok' : checkinStatus === 'error' ? 'danger' : checkinStatus === 'skipped' ? 'warn' : 'muted'
  const checkinTitle = [
    t(rowCheckinLabel),
    account.last_checkin_at ? new Date(account.last_checkin_at).toLocaleString() : '',
    account.last_checkin_msg || '',
  ].filter(Boolean).join(' · ')

  return (
    <div ref={rootRef} className="acct-item" data-state={state} data-focus={focus ? 'true' : undefined}>
      <div className="acct-row">
        <span className="acct-mark" title={provider}>
          <ProviderMark provider={account.provider} size={14} />
        </span>
        <span className="acct-name">
          <span className="nm">{account.name || account.id}</span>
          {account.remote_uid ? <span className="tag mono">UID {account.remote_uid}</span> : null}
        </span>
        <span className="acct-state">
          <span className="status-dot" data-state={tone} />
          <span className="w truncate">{stateCopy}</span>
        </span>
        {/* 认证方式列：按钮打开认证模态（设备登录 / PAT / 回调）。 */}
        <span className="col-auth">
          {onToggleAuthPanel ? (
            <Button
              size="sm"
              variant="ghost"
              isDisabled={!account.enabled}
              onPress={onToggleAuthPanel}
              aria-label={t('authentication')}
            >
              <Key size={13} />
              <span className="truncate">{authLabelFor(account, t)}</span>
            </Button>
          ) : (
            <span className="text-xs text-tertiary">—</span>
          )}
        </span>
        {/* 优先级列：行内可编辑（账号级；不迁设置）。 */}
        <span className="col-priority">
          {onPriorityChange ? (
            <NumberField
              value={priority}
              onChange={(value) => {
                const next = value ?? priority
                if (next !== priority) onPriorityChange(next)
              }}
              minValue={1}
              maxValue={100}
              isDisabled={Boolean(busyKind)}
              aria-label={t('priority')}
            >
              <NumberField.Group>
                <NumberField.DecrementButton />
                <NumberField.Input />
                <NumberField.IncrementButton />
              </NumberField.Group>
            </NumberField>
          ) : (
            <span className="mono text-xs">{priority}</span>
          )}
        </span>
        {/* 接收模型请求（行内开关）。 */}
        <span className="col-sw-models">
          {onToggleModelRequests ? (
            <CompactSwitch
              isSelected={account.model_requests_enabled !== false}
              isDisabled={busyKind === 'toggle'}
              ariaLabel={t('modelRequests')}
              onChange={onToggleModelRequests}
            />
          ) : null}
        </span>
        <span className="col-sw-checkin">
          {onToggleAutoCheckin ? (
            <CompactSwitch
              isSelected={Boolean(account.auto_checkin)}
              isDisabled={Boolean(busyKind)}
              ariaLabel={t('autoCheckin')}
              onChange={onToggleAutoCheckin}
            />
          ) : null}
        </span>
        <span className="ck-result">
          {onCheckin ? (
            <span className="ck-state" title={checkinTitle}>
              <span className="status-dot" data-state={checkinTone} />
              <span className="w truncate">{t(rowCheckinLabel)}</span>
            </span>
          ) : null}
        </span>
        <span className="ck-now">
          {onCheckin ? (
            <Tooltip>
              <Tooltip.Trigger>
                <Button
                  isIconOnly
                  size="sm"
                  variant="ghost"
                  isPending={busyKind === 'checkin'}
                  isDisabled={!account.enabled || busyKind === 'checkin'}
                  onPress={onCheckin}
                  aria-label={t('checkinNow')}
                >
                  <CalendarCheck size={14} />
                </Button>
              </Tooltip.Trigger>
              <Tooltip.Content>{account.enabled ? t('checkinNow') : t('disabled')}</Tooltip.Content>
            </Tooltip>
          ) : null}
        </span>
        <span className="quota-cell">
          {quota ? (
            <>
              <span className="quota-mini" aria-hidden="true">
                <span className="quota-mini__fill" data-tone={quota.tone} style={{ width: `${quota.remaining}%` }} />
              </span>
              <span className="mono text-xs text-secondary">{quota.exceeded ? t('quotaExceeded') : `${quota.remaining}%`}</span>
              {quotaAmount ? (
                <span className="mono shrink-0 text-xs text-tertiary" title={quotaAmountTitle}>{quotaAmount}</span>
              ) : null}
            </>
          ) : (
            <span className="text-xs text-tertiary">{state === 'hot' || state === 'ready' || state === 'loading' || state === 'starting' ? t('quotaLoading') : t('quotaUnavailable')}</span>
          )}
        </span>
        <span className="acct-error">
          {lastError ? (
            <Tooltip>
              <Tooltip.Trigger className="flex min-w-0 items-center gap-1.5 cursor-help">
                <span>
                  <WarningCircle size={13} className="shrink-0 text-danger" />
                </span>
                <span className="truncate">{lastError}</span>
              </Tooltip.Trigger>
              <Tooltip.Content>
                <div className="max-w-md whitespace-pre-wrap break-words">
                  {errorKind ? <div className="mono mb-1 text-micro opacity-75">{errorKind}</div> : null}
                  {lastError}
                </div>
              </Tooltip.Content>
            </Tooltip>
          ) : (
            <span className="text-xs text-tertiary">{t('noRecentError')}</span>
          )}
        </span>
        <span className="col-refresh">
          {onRefresh ? (
            <Tooltip>
              <Tooltip.Trigger>
                <Button isIconOnly size="sm" variant="ghost" isPending={busyKind === 'refresh'} onPress={onRefresh} aria-label={t('refreshAccount')}>
                  <ArrowClockwise size={14} />
                </Button>
              </Tooltip.Trigger>
              <Tooltip.Content>{t('refreshAccount')}</Tooltip.Content>
            </Tooltip>
          ) : null}
        </span>
        {/* ⋯ 溢出菜单（自展开区迁入）：查看模型 / 清冷却 / 导出 / 签到记录 / 重命名。 */}
        <span className="col-more">
          <Dropdown>
            <Dropdown.Trigger className="button button--icon-only button--sm button--ghost" aria-label={t('more')}>
              <DotsThreeVertical size={16} />
            </Dropdown.Trigger>
            <Dropdown.Popover placement="bottom end">
              <Dropdown.Menu
                aria-label={t('more')}
                onAction={(key) => {
                  if (key === 'models') onViewModels()
                  if (key === 'cooldowns') onClearCooldowns?.()
                  if (key === 'export') onExport()
                  if (key === 'checkins') onViewCheckins?.()
                  if (key === 'rename') onRename?.()
                }}
              >
                <Dropdown.Item id="models" textValue={t('accountModels')}><Cube size={15} />{t('accountModels')}</Dropdown.Item>
                {onClearCooldowns && (cooldown || modelCooldowns.length > 0) ? (
                  <Dropdown.Item id="cooldowns" textValue={t('clearCooldown')}><ArrowClockwise size={15} />{t('clearCooldown')}</Dropdown.Item>
                ) : null}
                {account.auth_type !== 'none' ? <Dropdown.Item id="export" textValue={t('export')}><Copy size={15} />{t('export')}</Dropdown.Item> : null}
                {onViewCheckins ? <Dropdown.Item id="checkins" textValue={t('checkinRecords')}><ListBullets size={15} />{t('checkinRecords')}</Dropdown.Item> : null}
                {onRename ? <Dropdown.Item id="rename" textValue={t('renameAccount')}><PencilSimple size={15} />{t('renameAccount')}</Dropdown.Item> : null}
              </Dropdown.Menu>
            </Dropdown.Popover>
          </Dropdown>
        </span>
        <span className="col-enabled">
          <CompactSwitch
            isSelected={Boolean(account.enabled)}
            isDisabled={busyKind === 'toggle'}
            ariaLabel={account.enabled ? t('disable') : t('enable')}
            onChange={onToggle}
          />
          <Button isIconOnly size="sm" variant="ghost" className="text-danger" isDisabled={Boolean(busyKind)} onPress={onDelete} aria-label={t('delete')}>
            <TrashSimple size={14} />
          </Button>
        </span>
      </div>
    </div>
  )
}
