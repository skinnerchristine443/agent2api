import { Button, Card, Chip } from '@heroui/react'
import { ArrowCircleUp } from '@phosphor-icons/react'
import { useI18n } from '@/hooks/I18nContext'
import type { SystemUpdateFlow } from '@/hooks/useSystemUpdate'

/**
 * 版本更新状态机 UI（方案 §4.4 ⑪ ②③）：当前 / 最新版本、主操作（下载 → 立即更新）、
 * 进行中 2s 轮询的状态框（状态文案 · 目标版本 · 已用时 · 倒计时自动刷新）与失败原文。
 */
export function UpdateStatusCard({ flow }: { flow: SystemUpdateFlow }) {
  const { t } = useI18n()
  const {
    info,
    applying,
    preparing,
    readyToApply,
    busy,
    submitting,
    reloadIn,
    justUpdated,
    updateStateText,
    statusHint,
    elapsed,
    targetVersion,
    newerThanPrepared,
    canPrepare,
    canApply,
    canCancel,
  } = flow
  const primaryLabel = reloadIn != null
    ? t('updateReloadingIn', { seconds: reloadIn })
    : canApply
      ? t('applyUpdateNow')
      : applying
        ? t('updateInProgress')
        : preparing || submitting
          ? t('updatePreparingImage')
          : t('updateNow')

  return (
    <Card data-gsap-reveal className="overflow-hidden p-0">
      <div className="flex items-center justify-between gap-3 border-b border-separator px-5 py-4">
        <div>
          <h3 className="font-semibold tracking-[-0.015em]">{t('versionUpdate')}</h3>
          <p className="mt-0.5 text-xs text-muted">{t('latestVersionHint')}</p>
        </div>
        <Chip size="sm" variant="soft" color={info?.has_update ? 'warning' : 'success'}>
          {info?.has_update ? t('updateAvailable') : t('upToDate')}
        </Chip>
      </div>

      <div className="px-5 py-5">
        <div className="grid overflow-hidden rounded-lg border border-separator sm:grid-cols-[1fr_auto_1fr]">
          <div className="p-4">
            <div className="text-xs font-medium text-muted">{t('currentVersion')}</div>
            <div className="mono mt-2 text-xl font-semibold">{info?.current_version || '—'}</div>
          </div>
          <div className="hidden items-center border-x border-separator px-4 text-muted sm:flex">
            <ArrowCircleUp size={18} />
          </div>
          <div className="border-t border-separator p-4 sm:border-t-0">
            <div className="text-xs font-medium text-muted">{t('latestVersion')}</div>
            <div className="mono mt-2 text-xl font-semibold">{info?.next_version || '—'}</div>
          </div>
        </div>

        <div className="mt-5 flex flex-wrap items-center justify-end gap-3">
          {canCancel ? (
            <Button size="sm" variant="ghost" isDisabled={submitting} onPress={() => void flow.cancelPreparedUpdate()}>
              {readyToApply ? t('discardPreparedImage') : t('cancelUpdate')}
            </Button>
          ) : null}
          <Button isDisabled={(!canPrepare && !canApply) || reloadIn != null} isPending={submitting || busy || reloadIn != null} onPress={() => void (canApply ? flow.confirmUpdate() : flow.prepareUpdate())}>
            <ArrowCircleUp size={16} />
            {primaryLabel}
          </Button>
        </div>

        {readyToApply || busy || reloadIn != null || justUpdated ? (
          <div className={`mt-4 rounded-lg border px-3 py-3 ${readyToApply || justUpdated ? 'border-success/25 bg-success/5' : 'border-warning/25 bg-warning/5'}`} role="status" aria-live="polite">
            {updateStateText ? (
              <p className="text-xs font-medium text-foreground">
                {updateStateText}
                {targetVersion ? ` · ${targetVersion}` : ''}
                {elapsed ? ` · ${t('updateElapsed', { seconds: elapsed })}` : ''}
              </p>
            ) : null}
            <p className="mt-1 text-xs leading-5 text-muted">{statusHint}</p>
            {newerThanPrepared ? <p className="mt-1 text-xs leading-5 text-warning">{t('updateNewerReleaseHint', { latest: info?.next_version || '' })}</p> : null}
            {info?.update?.error || info?.agent?.error ? <p className="mt-1 text-xs leading-5 text-danger">{info?.update?.error || info?.agent?.error}</p> : null}
          </div>
        ) : null}
      </div>
    </Card>
  )
}
