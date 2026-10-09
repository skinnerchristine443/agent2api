import type { Translate } from '@/i18n/messages'
import { useEffect, useRef, useState } from 'react'
import { Button, Dropdown, Input, Label, TextArea, TextField, Tooltip } from '@heroui/react'
import {
  ArrowClockwise,
  ArrowSquareOut,
  CalendarCheck,
  CaretDown,
  Copy,
  Cube,
  DotsThreeVertical,
  Key,
  ListBullets,
  PencilSimple,
  ShieldCheck,
  TrashSimple,
  WarningCircle,
} from '@phosphor-icons/react'
import { ProviderMark } from '@/components/brand/ProviderMark'
import { CompactSwitch } from '@/components/ui/CompactSwitch'
import { QuotaMeter } from '@/components/accounts/QuotaMeter'
import { RuntimeMeter } from '@/components/accounts/RuntimeMeter'
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
  authPanelOpen: boolean
  /** 收件箱跳转的目标行：初始展开 + 焦点高亮 + 首次滚动到位。 */
  focus?: boolean
  authUrl?: string
  note?: string
  pat: string
  t: Translate
  onPatChange: (value: string) => void
  // 当 provider 的能力表明该操作不可用时省略。未实现该流程的
  // provider 会返回 provider_unsupported，因此渲染该控件
  // 只会提供一个必然失败的按钮。
  onDeviceLogin?: () => void
  onPatLogin?: () => void
  callbackUrl?: string
  onCallbackChange?: (value: string) => void
  onSubmitCallback?: () => void
  onExport: () => void
  onRefresh?: () => void
  onDelete: () => void
  onToggle: (selected: boolean) => void
  onToggleModelRequests?: (selected: boolean) => void
  onToggleAutoCheckin?: (selected: boolean) => void
  onCheckin?: () => void
  onViewCheckins?: () => void
  onClearCooldowns?: () => void
  onEdit: () => void
  onToggleAuthPanel?: () => void
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

/**
 * 账号池行（状态卡）：40px 紧凑行承载「状态 / 额度 / 一行错误」，
 * hover 浮现主操作（刷新 / 启停），点击展开详情（含登录、开关与分级操作）。
 */
export function AccountRowItem({
  account,
  busyKind,
  authPanelOpen,
  focus,
  authUrl,
  note,
  pat,
  t,
  onPatChange,
  onDeviceLogin,
  onPatLogin,
  callbackUrl,
  onCallbackChange,
  onSubmitCallback,
  onExport,
  onRefresh,
  onDelete,
  onToggle,
  onToggleModelRequests,
  onToggleAutoCheckin,
  onCheckin,
  onViewCheckins,
  onClearCooldowns,
  onEdit,
  onToggleAuthPanel,
  onViewModels,
}: Props) {
  const [open, setOpen] = useState(Boolean(focus))
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
  // 行内额度：绝对剩余（批次 7 从展开卡上移；tooltip 带单位）。
  const quotaRemainValue = account.quota?.remaining
  const quotaUnit = account.quota?.unit || ''
  const quotaAmount = quotaRemainValue != null && Number.isFinite(quotaRemainValue) ? formatQuotaAmount(quotaRemainValue) : ''
  const quotaAmountTitle = quotaAmount ? `${t('quotaRemaining')} ${quotaAmount}${quotaUnit ? ` ${quotaUnit}` : ''}` : ''
  const checkinLabel = checkinStatus === 'success' ? 'checkinRecordSuccess' : checkinStatus === 'already' ? 'checkinRecordAlready' : checkinStatus === 'skipped' ? 'checkinRecordSkipped' : checkinStatus === 'error' ? 'checkinRecordFailed' : 'lastCheckinNone'
  // 行内签到列（批次 8）：短标签 + 状态点色；完整记录仍在展开卡 / 签到记录弹层。
  const rowCheckinLabel = checkinStatus === 'success' ? 'checkinRecordSuccess' : checkinStatus === 'already' ? 'checkinRecordAlready' : checkinStatus === 'skipped' ? 'checkinRecordSkipped' : checkinStatus === 'error' ? 'checkinRecordFailed' : 'acctCheckinNone'
  const checkinTone = checkinStatus === 'success' || checkinStatus === 'already' ? 'ok' : checkinStatus === 'error' ? 'danger' : checkinStatus === 'skipped' ? 'warn' : 'muted'
  const checkinTitle = [
    t(rowCheckinLabel),
    account.last_checkin_at ? new Date(account.last_checkin_at).toLocaleString() : '',
    account.last_checkin_msg || '',
  ].filter(Boolean).join(' · ')

  function toggleOpen() {
    setOpen((value) => !value)
  }

  return (
    <div ref={rootRef} className="acct-item" data-state={state} data-focus={focus ? 'true' : undefined}>
      {/* 行条：只承载「状态 > 额度 > 错误」三级扫读信息 */}
      <div
        className="acct-row"
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={(event) => {
          if ((event.target as HTMLElement).closest('button, input, label, a, select, .switch')) return
          toggleOpen()
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            toggleOpen()
          }
        }}
      >
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
        {/* 行内开关列（批次 9 拆列；批次 11 顺序对齐表头：接收模型请求 → 自动签到）；
            ≥1280px 展示，以下宽度隐藏并回落展开卡同名开关。 */}
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
        {/* 签到结果 + 立即签到（批次 9 拆列；按渠道能力出现，无能力时占位保持列对齐）。 */}
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
        {/* 操作拆列（批次 9）：刷新 / 启用状态（开关不再带文字标签，表头已注明）。 */}
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
        <span className="col-enabled">
          <CompactSwitch
            isSelected={Boolean(account.enabled)}
            isDisabled={busyKind === 'toggle'}
            ariaLabel={account.enabled ? t('disable') : t('enable')}
            onChange={onToggle}
          />
          <CaretDown size={12} className={`shrink-0 text-tertiary transition-transform duration-200 ${open ? 'rotate-180' : ''}`} />
        </span>
      </div>

      {open ? (
        <div className="acct-detail">
          <div className="detail-inner">
            {authPanelOpen && account.enabled && (onDeviceLogin || onPatLogin) ? (
              <section className="detail-auth">
                {onDeviceLogin ? (
                  <div>
                    <div className="text-xs font-medium text-secondary">{t('oauthDeviceFlow')}</div>
                    <p className="mt-1.5 text-xs leading-5 text-secondary">{t('accountLoginHint')}</p>
                    <div className="mt-2.5 flex flex-wrap gap-2">
                      <Button size="sm" isPending={busyKind === 'device'} onPress={onDeviceLogin}><ShieldCheck size={14} />{t('startBrowserLogin')}</Button>
                      {authUrl ? <Button size="sm" variant="ghost" onPress={() => window.open(authUrl, '_blank', 'noopener,noreferrer')}><ArrowSquareOut size={14} />{t('open')}</Button> : null}
                    </div>
                    {account.provider === 'trae' && onSubmitCallback && onCallbackChange ? (
                      <div className="mt-3 space-y-2">
                        <p className="text-micro leading-4 text-secondary">{t('wizardCallbackLead')}</p>
                        <TextArea
                          className="h-24 w-full resize-none font-mono text-xs leading-5"
                          value={callbackUrl || ''}
                          onChange={(event) => onCallbackChange(event.target.value)}
                          placeholder={t('wizardCallbackPh')}
                          aria-label={t('wizardCallbackPh')}
                        />
                        <Button size="sm" variant="secondary" isPending={busyKind === 'callback'} onPress={onSubmitCallback}>
                          {t('wizardSubmitCallback')}
                        </Button>
                      </div>
                    ) : null}
                  </div>
                ) : null}
                {onPatLogin ? (
                  <div>
                    <div className="text-xs font-medium text-secondary">{t('patFallback')}</div>
                    <div className="mt-2.5 flex flex-col gap-2 sm:flex-row">
                      <TextField className="flex-1" type="password" value={pat} onChange={onPatChange}>
                        <Label className="sr-only">{t('pat')}</Label>
                        <Input placeholder={t('pasteToken')} aria-label={t('pat')} />
                      </TextField>
                      <Button size="sm" variant="secondary" isPending={busyKind === 'pat'} onPress={onPatLogin}><Key size={14} />{t('usePat')}</Button>
                    </div>
                  </div>
                ) : null}
                {authUrl || note ? (
                  <div className="text-xs">
                    {authUrl ? <code className="mono block break-all text-secondary">{authUrl}</code> : null}
                    {note ? <p className="mt-1 text-secondary">{note}</p> : null}
                  </div>
                ) : null}
              </section>
            ) : null}

            <RuntimeMeter state={state} stateCopy={stateCopy} t={t} />

            <div className="detail-kv">
              <div className="kv"><span className="k">UID</span><span className="v mono truncate" title={account.id}>{account.remote_uid || account.id}</span></div>
              <div className="kv"><span className="k">{t('priority')}</span><span className="v mono">{account.priority ?? '—'}</span></div>
              <div className="kv"><span className="k">{t('maxInflight')}</span><span className="v mono">{account.max_inflight ?? '—'}</span></div>
              <div className="kv"><span className="k">{t('inFlight')}</span><span className="v mono">{account.in_flight ?? account.inFlight ?? 0}</span></div>
              <div className="kv"><span className="k">{t('dailyGuardReserve')}</span><span className="v mono">{formatQuotaAmount(account.reserve_credits)}</span></div>
              <div className="kv"><span className="k">{t('dailyGuardTokenLimit')}</span><span className="v mono">{formatQuotaAmount(account.daily_token_limit)}</span></div>
              <div className="kv"><span className="k">{t('dailyGuardCreditLimit')}</span><span className="v mono">{formatQuotaAmount(account.daily_credit_limit)}</span></div>
              <div className="kv"><span className="k">{t('dailyGuardModelTokenLimit')}</span><span className="v mono">{formatQuotaAmount(account.daily_model_token_limit)}</span></div>
            </div>

            {account.quota ? (
              <QuotaMeter
                quota={account.quota}
                t={t}
                label={t('quota')}
                usedLabel={t('quotaUsed')}
                remainingLabel={t('quotaRemaining')}
                addOnLabel={t('quotaAddOn')}
                resourcePackageLabel={t('quotaResourcePackage')}
                exceededLabel={t('quotaExceeded')}
                provider={account.provider}
              />
            ) : null}

            {lastError ? (
              <div className="flex gap-2 rounded-lg border border-danger/25 bg-danger/5 p-2 text-xs leading-5 text-danger">
                <WarningCircle size={14} className="mt-0.5 shrink-0" />
                <div className="min-w-0 flex-1">
                  {errorKind ? <div className="mono mb-0.5 truncate text-micro opacity-75">{errorKind}</div> : null}
                  <p className="break-words">{lastError}</p>
                </div>
              </div>
            ) : null}

            <div className="detail-rows">
              {onCheckin ? (
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-secondary">
                  <span>
                    {t('lastCheckin')}：
                    <span className={checkinStatus === 'error' ? 'text-danger' : 'text-foreground'}>
                      {t(checkinLabel)}
                    </span>
                    {account.last_checkin_at ? <span className="ml-1.5 text-tertiary" title={account.last_checkin_msg || ''}>{new Date(account.last_checkin_at).toLocaleString()}</span> : null}
                  </span>
                  {onToggleAutoCheckin ? (
                    <span className="row-switches-fallback flex items-center gap-1.5">
                      <span>{t('autoCheckin')}</span>
                      <CompactSwitch isSelected={Boolean(account.auto_checkin)} isDisabled={Boolean(busyKind)} ariaLabel={t('autoCheckin')} onChange={onToggleAutoCheckin} />
                    </span>
                  ) : null}
                </div>
              ) : null}
              {onToggleModelRequests ? (
                <div className="row-switches-fallback flex items-center gap-2 text-xs text-secondary">
                  <span>{t('modelRequests')}</span>
                  <CompactSwitch
                    isSelected={account.model_requests_enabled !== false}
                    isDisabled={busyKind === 'toggle'}
                    ariaLabel={t('modelRequests')}
                    onChange={onToggleModelRequests}
                  />
                </div>
              ) : null}
            </div>

            {/* 操作分级（设计规范 §6.7）：主组 = 认证 / 编辑；⋯ 溢出 = 查看模型 / 清冷却 / 导出 / 签到记录；删除隔离 */}
            <div className="detail-actions">
              {onToggleAuthPanel ? (
                <Button size="sm" variant="secondary" isDisabled={!account.enabled} onPress={onToggleAuthPanel}><Key size={14} />{t('authentication')}</Button>
              ) : null}
              <Button size="sm" variant="secondary" onPress={onEdit}><PencilSimple size={14} />{t('editAccount')}</Button>
              <Dropdown>
                {/* Trigger 自身即 button（RAC Button）：内容直接放图标，样式类别名与 HeroUI Button 一致。
                    不可再嵌套 <Button>——会生成 button 套 button 的非法结构（React 会报 hydration 错）。 */}
                <Dropdown.Trigger className="button button--icon-only button--sm button--secondary" aria-label={t('more')}>
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
                    }}
                  >
                    <Dropdown.Item id="models" textValue={t('accountModels')}><Cube size={15} />{t('accountModels')}</Dropdown.Item>
                    {onClearCooldowns && (cooldown || modelCooldowns.length > 0) ? (
                      <Dropdown.Item id="cooldowns" textValue={t('clearCooldown')}><ArrowClockwise size={15} />{t('clearCooldown')}</Dropdown.Item>
                    ) : null}
                    {account.auth_type !== 'none' ? <Dropdown.Item id="export" textValue={t('export')}><Copy size={15} />{t('export')}</Dropdown.Item> : null}
                    {onViewCheckins ? <Dropdown.Item id="checkins" textValue={t('checkinRecords')}><ListBullets size={15} />{t('checkinRecords')}</Dropdown.Item> : null}
                  </Dropdown.Menu>
                </Dropdown.Popover>
              </Dropdown>
              <span className="btn-divider" aria-hidden="true" />
              <Button isIconOnly size="sm" variant="ghost" className="text-danger" isDisabled={Boolean(busyKind)} onPress={onDelete} aria-label={t('delete')}>
                <TrashSimple size={15} />
              </Button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
