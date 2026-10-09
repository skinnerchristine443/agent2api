import { Chip } from '@heroui/react'

import type { RuntimeLogEntry } from '@/api/logs'
import { Sheet } from '@/components/ui/Sheet'
import { useI18n } from '@/hooks/I18nContext'

import { formatTime, runtimeLevelColor } from '@/lib/logsFormat'

/**
 * 运行日志详情 Sheet（`?entry=<n>` 寻址）：时间 / 级别 / 来源 / 账号 + 完整内容。
 *
 * 运行日志没有单条取数接口（后端只有列表 + after/offset），因此详情数据从
 * 当前页列表里按 id 找；直链打开时该条若已被新日志挤出当前页，显示
 * `logsRuntimeEntryMissing` 提示而不是空白。
 */
export function RuntimeDetailSheet({
  entry,
  isOpen,
  onOpenChange,
  missing,
}: {
  entry: RuntimeLogEntry | null
  isOpen: boolean
  onOpenChange: (isOpen: boolean) => void
  missing: boolean
}) {
  const { t } = useI18n()

  return (
    <Sheet
      isOpen={isOpen}
      onOpenChange={onOpenChange}
      closeLabel={t('close')}
      className="w-full max-w-2xl"
      title={(
        <span className="flex items-baseline gap-2">
          <span>{t('logsRuntimeDetailTitle')}</span>
          {entry ? <span className="mono text-micro text-muted">#{entry.id}</span> : null}
        </span>
      )}
    >
      {missing ? (
        <p className="text-sm leading-6 text-muted">{t('logsRuntimeEntryMissing')}</p>
      ) : entry ? (
        <div className="space-y-5">
          <dl className="grid gap-3 sm:grid-cols-2">
            <div>
              <dt className="text-micro text-muted">{t('logsColTime')}</dt>
              <dd className="mt-1 break-all text-sm font-medium">{formatTime(entry.time)}</dd>
            </div>
            <div>
              <dt className="text-micro text-muted">{t('logsRuntimeLevel')}</dt>
              <dd className="mt-1">
                <Chip size="sm" variant="soft" color={runtimeLevelColor(entry.level)}>{entry.level}</Chip>
              </dd>
            </div>
            <div>
              <dt className="text-micro text-muted">{t('logsRuntimeSource')}</dt>
              <dd className="mt-1 break-all text-sm font-medium">{entry.source || '—'}</dd>
            </div>
            <div>
              <dt className="text-micro text-muted">{t('logsColAccount')}</dt>
              <dd className="mt-1 break-all text-sm font-medium">{entry.account_id || '—'}</dd>
            </div>
          </dl>
          <section className="space-y-2">
            <div className="text-xs font-medium text-muted">{t('logsRuntimeMessage')}</div>
            <pre className="mono max-h-[min(38rem,60vh)] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-surface-secondary px-4 py-3 text-xs leading-6 text-foreground">
              {entry.message || '—'}
            </pre>
          </section>
        </div>
      ) : null}
    </Sheet>
  )
}
