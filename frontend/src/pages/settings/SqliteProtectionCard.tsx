import { CheckCircle, Database, ShieldCheck } from '@phosphor-icons/react'

import { SettingCard } from '@/components/ui/SettingCard'
import { useI18n } from '@/hooks/I18nContext'

/** SQLite 保护说明卡（静态文案：更新前校验备份 / 保留 5 份 / 失败时一并恢复）。 */
export function SqliteProtectionCard() {
  const { t } = useI18n()
  return (
    <SettingCard
      icon={<Database size={15} />}
      title={t('sqliteProtection')}
      hint={t('sqliteProtectionHint')}
      right={<ShieldCheck size={18} className="text-success" />}
    >
      <div className="mono rounded-lg bg-surface-secondary px-3 py-2 text-xs text-muted">/data</div>
      <div className="mt-3 grid gap-2 text-xs text-muted">
        <div className="flex items-center gap-2"><CheckCircle size={14} className="text-success" />{t('sqliteBackupBeforeUpdate')}</div>
        <div className="flex items-center gap-2"><CheckCircle size={14} className="text-success" />{t('sqliteKeepFive')}</div>
        <div className="flex items-center gap-2"><CheckCircle size={14} className="text-success" />{t('sqliteRollbackTogether')}</div>
      </div>
    </SettingCard>
  )
}
