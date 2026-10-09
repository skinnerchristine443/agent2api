import { useEffect, useRef, useState } from 'react'
import { Button, Card } from '@heroui/react'
import { Check, CopySimple, TerminalWindow } from '@phosphor-icons/react'

import type { RuntimeLogEntry } from '@/api/logs'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { LogsRuntimeListSkeleton } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'
import { copyText } from '@/lib/clipboard'

import { formatTime, levelDot } from '@/lib/logsFormat'

/** 行尾复制按钮：复制完整日志行（列表里长行会被截断，复制拿到的仍是原文）。 */
function CopyLineButton({ text, label, copiedLabel }: { text: string; label: string; copiedLabel: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => {
    if (timerRef.current != null) clearTimeout(timerRef.current)
  }, [])

  async function onCopy() {
    const ok = await copyText(text)
    if (!ok) return
    setCopied(true)
    if (timerRef.current != null) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => setCopied(false), 1200)
  }

  return (
    <span onClick={(event) => event.stopPropagation()}>
      <Button
        size="sm"
        variant="ghost"
        isIconOnly
        aria-label={copied ? copiedLabel : label}
        onPress={() => void onCopy()}
      >
        {copied ? <Check size={14} /> : <CopySimple size={14} />}
      </Button>
    </span>
  )
}

/** 行布局类：抽常量避免超长 className 行（约定护栏 ③）。 */
const RUNTIME_ROW_CLASS = [
  'grid cursor-pointer gap-2 px-5 py-3 transition-colors sm:items-start',
  'hover:bg-surface-secondary/55 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
  'sm:grid-cols-[150px_72px_minmax(0,1fr)_auto]',
].join(' ')

/**
 * 运行日志列表：时间 / 级别 / 账号 / 内容（长行截断 + 一键复制原文）。
 * 行点击 = 打开详情 Sheet（写入 `?entry=<id>`）；空态区分「无日志」与
 * 「筛选后无结果」。
 */
export function LogsRuntimeList({
  items,
  loading,
  hasFilters,
  onOpenEntry,
  onClearFilters,
}: {
  items: RuntimeLogEntry[]
  loading: boolean
  hasFilters: boolean
  onOpenEntry: (id: number) => void
  onClearFilters: () => void
}) {
  const { t } = useI18n()

  return (
    <Card data-gsap-reveal className="overflow-hidden p-0" aria-busy={loading}>
      {loading ? (
        <LogsRuntimeListSkeleton />
      ) : items.length === 0 ? (
        <EmptyPanel
          icon={<TerminalWindow size={22} />}
          title={hasFilters ? t('logsRuntimeEmptyFiltered') : t('logsEmptyRuntime')}
          action={hasFilters ? <Button size="sm" variant="ghost" onPress={onClearFilters}>{t('clearFilters')}</Button> : null}
        />
      ) : (
        <div className="divide-y divide-separator">
          {items.map((entry) => (
            <div
              key={entry.id}
              className={RUNTIME_ROW_CLASS}
              role="button"
              tabIndex={0}
              onClick={() => onOpenEntry(entry.id)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault()
                  onOpenEntry(entry.id)
                }
              }}
            >
              <div className="mono text-micro text-muted">{formatTime(entry.time)}</div>
              <div className="flex items-center gap-2 text-xs">
                <span className="status-dot" data-state={levelDot(entry.level)} />
                <span className="font-medium text-muted">{entry.level}</span>
              </div>
              <div className="min-w-0">
                {entry.account_id ? <div className="mono mb-1 text-micro text-muted">{entry.account_id}</div> : null}
                <div className="mono line-clamp-2 break-all text-xs leading-5 text-foreground">{entry.message}</div>
              </div>
              <CopyLineButton text={entry.message} label={t('logsCopyLine')} copiedLabel={t('logsCopied')} />
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}
