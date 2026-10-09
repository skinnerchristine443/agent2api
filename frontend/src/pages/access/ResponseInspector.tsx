import { Chip, Skeleton } from '@heroui/react'
import { BracketsCurly, CheckCircle, Clock } from '@phosphor-icons/react'

import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { PageAlert } from '@/components/ui/PageAlert'
import { useI18n } from '@/hooks/I18nContext'
import type { AccessData } from '@/hooks/useAccessData'

/** 调试台右栏：原始响应检查（完成态 JSON / 失败态错误体原文 / 耗时）。 */
export function ResponseInspector({ data }: { data: AccessData }) {
  const { t } = useI18n()
  const responseStatus = data.requestState === 'success'
    ? t('requestComplete')
    : data.requestState === 'error'
      ? t('requestFailed')
      : data.requestState === 'loading'
        ? t('requesting')
        : t('requestIdle')

  return (
    <div
      className="flex min-h-[620px] min-w-0 flex-col bg-surface-secondary/45"
      aria-live="polite"
      aria-busy={data.requestState === 'loading'}
    >
      <div className="flex min-h-16 items-center justify-between gap-4 border-b border-separator px-5 py-4 sm:px-6">
        <div>
          <h3 className="font-semibold tracking-[-0.015em]">{t('responseInspector')}</h3>
          <p className="mt-0.5 text-xs text-muted">{t('responseInspectorHint')}</p>
        </div>
        <div className="flex items-center gap-2">
          {data.elapsedMs !== null ? (
            <span className="mono flex items-center gap-1.5 text-micro text-muted">
              <Clock size={12} />
              {data.elapsedMs} ms
            </span>
          ) : null}
          <Chip
            size="sm"
            variant="soft"
            color={data.requestState === 'success' ? 'success' : data.requestState === 'error' ? 'danger' : undefined}
          >
            {responseStatus}
          </Chip>
        </div>
      </div>

      <div className="min-h-0 flex-1 p-5 sm:p-6">
        {data.requestState === 'idle' ? (
          <EmptyPanel
            className="min-h-80"
            icon={<BracketsCurly size={21} />}
            title={t('responseEmptyTitle')}
            hint={t('responseEmptyHint')}
          />
        ) : data.requestState === 'loading' ? (
          <div className="space-y-3 pt-1">
            <Skeleton className="h-3 w-32 rounded-lg" />
            <Skeleton className="h-3 w-full rounded-lg" />
            <Skeleton className="h-3 w-[88%] rounded-lg" />
            <Skeleton className="h-3 w-[72%] rounded-lg" />
            <Skeleton className="mt-7 h-3 w-[92%] rounded-lg" />
            <Skeleton className="h-3 w-[64%] rounded-lg" />
          </div>
        ) : data.requestState === 'error' ? (
          <div className="space-y-3">
            <PageAlert status="danger" title={t('requestFailed')} description={data.output} />
          </div>
        ) : (
          <div>
            <div className="mb-4 flex items-center gap-2 text-xs font-medium text-success">
              <CheckCircle size={15} weight="fill" />
              {t('responseReceived')}
            </div>
            <pre className="mono overflow-x-auto whitespace-pre-wrap break-words text-xs leading-6 text-muted">
              {data.output}
            </pre>
          </div>
        )}
      </div>
    </div>
  )
}
