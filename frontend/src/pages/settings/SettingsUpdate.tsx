import { useEffect, useState } from 'react'
import { Button } from '@heroui/react'
import { ArrowClockwise } from '@phosphor-icons/react'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { PageAlert } from '@/components/ui/PageAlert'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'
import { useSystemUpdate } from '@/hooks/useSystemUpdate'

import { SqliteProtectionCard } from './SqliteProtectionCard'
import { UpdateStatusCard } from './UpdateStatusCard'
import { UpdaterAvailabilityCard } from './UpdaterAvailabilityCard'
import { VersionHistoryCard } from './VersionHistoryCard'

/**
 * 设置 · 更新页签（批次 4a 收编，原版本更新页）：
 * 更新器可用性诊断 → 当前/最新版本与状态机（2s 条件轮询，完成即停）→
 * 版本历史（可回滚）→ SQLite 保护说明。检查更新走 `?force=1`。
 */
export function SettingsUpdate() {
  const { t } = useI18n()
  const [error, setError] = useState('')
  const flow = useSystemUpdate(t, setError)
  const { load } = flow

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load(false)
    }, 0)
    return () => window.clearTimeout(timer)
  }, [load])

  if (flow.loading && !flow.info) {
    return (
      <div className="space-y-4">
        <SkeletonBlock className="h-16 w-full rounded-2xl" />
        <SkeletonBlock className="h-56 w-full rounded-2xl" />
        <SkeletonBlock className="h-40 w-full rounded-2xl" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      {error ? <PageAlert title={error} /> : null}

      <div className="flex justify-end">
        <Button size="sm" variant="secondary" isPending={flow.checking} onPress={() => void load(true)}>
          <ArrowClockwise size={15} />{t('checkUpdates')}
        </Button>
      </div>

      <UpdaterAvailabilityCard info={flow.info} />
      <UpdateStatusCard flow={flow} />
      <VersionHistoryCard flow={flow} onRestore={flow.setRestoreTarget} />
      <SqliteProtectionCard />

      <ConfirmDialog
        isOpen={Boolean(flow.restoreTarget)}
        title={t('restoreVersionTitle', { version: flow.restoreTarget })}
        description={t('restoreVersionHint')}
        confirmLabel={t('restoreThisVersion')}
        cancelLabel={t('cancel')}
        closeLabel={t('close')}
        isPending={flow.submitting}
        status="warning"
        confirmVariant="primary"
        onClose={() => { if (!flow.submitting) flow.setRestoreTarget('') }}
        onConfirm={() => {
          const version = flow.restoreTarget
          flow.setRestoreTarget('')
          void flow.rollbackTo(version)
        }}
      />
    </div>
  )
}
