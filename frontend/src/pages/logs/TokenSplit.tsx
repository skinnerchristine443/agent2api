import type { RequestLog } from '@/api/logs'

import { formatCredit } from '@/lib/logsFormat'

/**
 * Token 单元格：输入 / 输出 + （可选）消耗点数。
 * 表格行与请求详情共用，避免两处口径漂移。
 */
export function TokenSplit({
  log,
  inLabel,
  outLabel,
  pointsLabel,
}: {
  log: RequestLog
  inLabel: string
  outLabel: string
  pointsLabel?: (value: string) => string
}) {
  const prompt = log.prompt_tokens
  const completion = log.completion_tokens
  const credit = log.credits ?? log.usage_detail?.credit
  const creditText = credit != null && Number.isFinite(credit) ? formatCredit(credit) : null
  if (prompt == null && completion == null && creditText == null) {
    return <span className="mono text-xs text-muted">—</span>
  }
  return (
    <div className="leading-4">
      {prompt != null || completion != null ? (
        <>
          <div className="mono text-xs">{prompt ?? 0} / {completion ?? 0}</div>
          <div className="mt-0.5 text-micro text-muted">{inLabel} / {outLabel}</div>
        </>
      ) : null}
      {creditText != null ? (
        <div className={`mono text-micro text-success ${prompt != null || completion != null ? 'mt-0.5' : ''}`}>
          {pointsLabel ? pointsLabel(creditText) : creditText}
        </div>
      ) : null}
    </div>
  )
}
