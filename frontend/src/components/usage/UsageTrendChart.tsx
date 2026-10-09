import { useMemo } from 'react'
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { UsageStatsDaily } from '@/api/usage'
import { formatCompact } from '@/lib/format'

function dayLabel(date: string) {
  // 直接解析固定的 YYYY-MM-DD 键：`new Date('2026-08-28')` 是 UTC
  // 午夜，在负偏移时区会偏移一天。
  const parts = date.split('-')
  if (parts.length !== 3) return date
  const [, month, day] = parts
  return `${Number(month)}/${Number(day)}`
}

type Row = UsageStatsDaily & { prompt: number; completion: number; requests: number }

function UsageTooltip({
  active,
  payload,
  promptLabel,
  completionLabel,
  requestsLabel,
}: {
  active?: boolean
  payload?: Array<{ payload: Row }>
  promptLabel: string
  completionLabel: string
  requestsLabel: string
}) {
  if (!active || !payload?.[0]) return null
  const row = payload[0].payload
  return (
    <div className="rounded-xl border border-border bg-overlay px-3 py-2 text-micro text-overlay-foreground shadow-overlay">
      <div className="mono text-muted">{dayLabel(row.date)}</div>
      <div className="mt-1.5 grid gap-1">
        <div className="flex items-center justify-between gap-6">
          <span className="inline-flex items-center gap-1.5"><span className="size-1.5 rounded-full bg-accent" />{promptLabel}</span>
          <span className="mono font-medium">{formatCompact(row.prompt)}</span>
        </div>
        <div className="flex items-center justify-between gap-6">
          <span className="inline-flex items-center gap-1.5"><span className="size-1.5 rounded-full bg-success" />{completionLabel}</span>
          <span className="mono font-medium">{formatCompact(row.completion)}</span>
        </div>
        <div className="flex items-center justify-between gap-6 border-t border-separator pt-1">
          <span className="text-muted">{requestsLabel}</span>
          <span className="mono font-medium">{row.requests}</span>
        </div>
      </div>
    </div>
  )
}

export function UsageTrendChart({
  daily,
  promptLabel,
  completionLabel,
  requestsLabel,
}: {
  daily: UsageStatsDaily[]
  promptLabel: string
  completionLabel: string
  requestsLabel: string
}) {
  const data = useMemo<Row[]>(
    () => daily.map((day) => ({
      ...day,
      prompt: day.prompt_tokens,
      completion: day.completion_tokens,
      requests: day.requests,
    })),
    [daily],
  )
  const peakTokens = Math.max(0, ...data.map((row) => row.prompt + row.completion))

  return (
    <div className="h-72 w-full min-w-0">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid vertical={false} stroke="var(--separator)" strokeDasharray="3 6" />
          <XAxis
            dataKey="date"
            tickLine={false}
            axisLine={false}
            minTickGap={24}
            tick={{ fill: 'var(--muted)', fontSize: 10, fontFamily: 'var(--font-mono)' }}
            tickFormatter={(value: string) => dayLabel(value)}
          />
          <YAxis
            yAxisId="tokens"
            width={44}
            tickLine={false}
            axisLine={false}
            allowDecimals={false}
            tick={{ fill: 'var(--muted)', fontSize: 10, fontFamily: 'var(--font-mono)' }}
            tickFormatter={(value: number) => formatCompact(value)}
          />
          <YAxis
            yAxisId="requests"
            orientation="right"
            width={36}
            tickLine={false}
            axisLine={false}
            allowDecimals={false}
            tick={{ fill: 'var(--muted)', fontSize: 10, fontFamily: 'var(--font-mono)' }}
            tickFormatter={(value: number) => formatCompact(value)}
          />
          <Tooltip
            cursor={{ fill: 'var(--separator)', fillOpacity: 0.4 }}
            content={<UsageTooltip promptLabel={promptLabel} completionLabel={completionLabel} requestsLabel={requestsLabel} />}
          />
          <Bar yAxisId="tokens" dataKey="prompt" stackId="tokens" fill="var(--accent)" radius={[0, 0, 0, 0]} isAnimationActive={false} />
          <Bar yAxisId="tokens" dataKey="completion" stackId="tokens" fill="var(--success)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
          <Line yAxisId="requests" type="monotone" dataKey="requests" stroke="var(--muted)" strokeWidth={1.5} dot={false} isAnimationActive={false} />
        </ComposedChart>
      </ResponsiveContainer>
      <div className="mt-2 flex min-h-5 items-center justify-between gap-3 text-[10px] text-muted">
        <div className="flex items-center gap-3">
          <span className="inline-flex items-center gap-1.5"><span className="size-1.5 rounded-full bg-accent" />{promptLabel}</span>
          <span className="inline-flex items-center gap-1.5"><span className="size-1.5 rounded-full bg-success" />{completionLabel}</span>
          <span className="inline-flex items-center gap-1.5"><span className="h-0.5 w-3 rounded-full bg-muted" />{requestsLabel}</span>
        </div>
        <span className="mono">{formatCompact(peakTokens)} peak</span>
      </div>
    </div>
  )
}
