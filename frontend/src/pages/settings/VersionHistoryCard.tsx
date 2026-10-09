import { VersionHistory } from '@/components/system/VersionHistory'
import { useI18n } from '@/hooks/I18nContext'
import type { SystemUpdateFlow } from '@/hooks/useSystemUpdate'

/**
 * 版本历史（方案 §4.4 ⑪ ⑤）：按发布日期列出发布说明，可就地恢复到当前版本
 * 之前最近的三个版本；跨版本更新时提示「中间经过的版本」（backend skipped_versions）。
 */
export function VersionHistoryCard({ flow, onRestore }: { flow: SystemUpdateFlow; onRestore: (version: string) => void }) {
  const { t } = useI18n()
  const { info, historyReleases, rollbackTags, canRollback, submitting } = flow
  if (!historyReleases.length) return null

  return (
    <div data-gsap-reveal className="rounded-2xl border border-border bg-surface p-4">
      <p className="text-xs leading-5 text-muted">{t('updateHistoryHint')}</p>
      {info?.skipped_versions?.length ? (
        <p className="mt-2 rounded-lg border border-warning/25 bg-warning/5 px-3 py-2 text-xs leading-5 text-muted">
          {t('skippedBetweenLabel')}
          {' '}
          <span className="mono">{info.skipped_versions.join(' → ')}</span>
        </p>
      ) : null}
      <VersionHistory
        releases={historyReleases}
        currentVersion={info?.current_version || ''}
        nextVersion={info?.next_version}
        rollbackTags={rollbackTags}
        canRollback={canRollback}
        submitting={submitting}
        onRestore={onRestore}
      />
    </div>
  )
}
