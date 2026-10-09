import { SegmentedNav } from '@/components/ui/SegmentedNav'
import { useI18n } from '@/hooks/I18nContext'

/**
 * 观测页内 tab 条（批次 4b 收编）：请求日志 / 运行日志 / 用量统计——
 * 三页共享（跨路径切换，`menu: false` 的从属路径不再进侧栏，导航收口于此）。
 */
export function ObserveTabs() {
  const { t } = useI18n()
  return (
    <SegmentedNav
      ariaLabel={t('navObserve')}
      items={[
        { to: '/logs/requests', label: t('navLogsRequests') },
        { to: '/logs/runtime', label: t('navLogsRuntime') },
        { to: '/usage', label: t('navUsage') },
      ]}
    />
  )
}
