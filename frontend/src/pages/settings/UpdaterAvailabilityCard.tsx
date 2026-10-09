import { Wrench } from '@phosphor-icons/react'
import type { SystemUpdateInfo } from '@/api/system'
import { Banner } from '@/components/ui/Banner'
import { SectionCard } from '@/components/ui/SectionCard'
import { useI18n } from '@/hooks/I18nContext'

import { updateAvailability } from './updateAvailability'

/**
 * 更新器可用性前置诊断：这台机器能否更新；不能更新时列出原因与修复指引。
 * （Docker 无头部署下「宿主机更新器 + 已发布 Release」是更新成功的前提。）
 */
export function UpdaterAvailabilityCard({ info }: { info: SystemUpdateInfo | null }) {
  const { t } = useI18n()
  const { ready, reasons } = updateAvailability(info)
  if (!info) return null

  return (
    <div data-gsap-reveal className="space-y-3">
      <Banner
        status={ready ? 'success' : 'warning'}
        title={ready ? t('updaterReady') : t('updaterUnavailable')}
        description={ready ? undefined : t('updaterUnavailableHint')}
      />
      {ready ? (
        info.agent?.staged_update ? null : <p className="px-1 text-xs leading-5 text-muted">{t('updateLegacyOneShotHint')}</p>
      ) : (
        <SectionCard title={t('updaterFixTitle')} right={<Wrench size={16} className="text-muted" />} padded={false}>
          <ul className="divide-y divide-separator">
            {reasons.map((key) => (
              <li key={key} className="px-4 py-2.5 text-xs leading-5 text-muted">{t(key)}</li>
            ))}
          </ul>
        </SectionCard>
      )}
    </div>
  )
}
