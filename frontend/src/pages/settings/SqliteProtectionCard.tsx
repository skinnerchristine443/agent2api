import { Card } from '@heroui/react'
import { CheckCircle, Database, ShieldCheck } from '@phosphor-icons/react'

import { useI18n } from '@/hooks/I18nContext'

/** SQLite 保护说明卡（静态文案：更新前校验备份 / 保留 5 份 / 失败时一并恢复）。 */
export function SqliteProtectionCard() {
  const { t } = useI18n()
  return (
    <Card data-gsap-reveal>
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <div className="grid size-8 shrink-0 place-items-center rounded-lg bg-surface-secondary text-foreground"><Database size={15} /></div>
          <div>
            <h3 className="font-semibold">{t('sqliteProtection')}</h3>
            <p className="mt-1 text-xs leading-5 text-muted">{t('sqliteProtectionHint')}</p>
          </div>
        </div>
        <ShieldCheck size={18} className="text-success" />
      </div>
      <div className="mono mt-4 rounded-lg bg-surface-secondary px-3 py-2 text-xs text-muted">/data</div>
      <div className="mt-3 grid gap-2 text-xs text-muted">
        <div className="flex items-center gap-2"><CheckCircle size={14} className="text-success" />{t('sqliteBackupBeforeUpdate')}</div>
        <div className="flex items-center gap-2"><CheckCircle size={14} className="text-success" />{t('sqliteKeepFive')}</div>
        <div className="flex items-center gap-2"><CheckCircle size={14} className="text-success" />{t('sqliteRollbackTogether')}</div>
      </div>
    </Card>
  )
}
