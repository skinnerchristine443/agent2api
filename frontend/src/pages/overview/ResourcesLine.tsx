import type { SystemResources } from '@/api/system'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { StatusDot } from '@/components/ui/StatusDot'
import { useI18n } from '@/hooks/I18nContext'
import { formatBytes } from '@/lib/format'

function resourcesTone(rssBytes: number, cpuPercent: number): 'ok' | 'warn' | 'danger' {
  if (rssBytes >= 2 * 1024 * 1024 * 1024 || cpuPercent >= 200) return 'danger'
  if (rssBytes >= 1024 * 1024 * 1024 || cpuPercent >= 100) return 'warn'
  return 'ok'
}

/**
 * 进程资源细行（`/api/system/resources`，5s 轮询由页面持有）：
 * 工作台最底部的次级信息——单行呈现 RSS / CPU / goroutines / 采样时间
 * （v1 独立卡片收敛，密度治理 §3.1；数值口径不变）。
 */
export function ResourcesLine({ resources }: { resources: SystemResources | null }) {
  const { t } = useI18n()
  if (!resources) {
    return (
      <div className="px-1">
        <SkeletonBlock className="h-3 w-64" />
      </div>
    )
  }
  const server = resources.server
  return (
    <div data-gsap-reveal className="flex flex-wrap items-center gap-x-4 gap-y-1 px-1 text-micro text-muted">
      <span className="inline-flex items-center gap-1.5">
        <StatusDot className="shrink-0" state={resourcesTone(server?.rss_bytes ?? 0, server?.cpu_percent ?? 0)} />
        {t('resTitle')}
      </span>
      <span className="mono">RSS {formatBytes(server?.rss_bytes)}</span>
      <span className="mono">CPU {(server?.cpu_percent ?? 0).toFixed(1)}%</span>
      <span className="mono">{server?.goroutines ?? 0} {t('resGoroutines')}</span>
      <span className="mono ml-auto">
        pid {server?.pid} · {resources.sampled_at ? new Date(resources.sampled_at).toLocaleTimeString() : '—'}
      </span>
    </div>
  )
}
