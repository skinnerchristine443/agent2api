import { Key, Plugs } from '@phosphor-icons/react'
import { SectionCard } from '@/components/ui/SectionCard'
import { useI18n } from '@/hooks/I18nContext'

/** 双钥语义与定位边界：管理面 / 数据面分工、失效时机、以及「不新增/不分组/不分发」。 */
export function KeySemanticsCard() {
  const { t } = useI18n()
  return (
    <div data-gsap-reveal>
      <SectionCard title={t('keysSemanticsTitle')} hint={t('keysSemanticsHint')} padded={false}>
        <div className="divide-y divide-separator">
          <div className="px-4 py-3">
            <div className="flex items-center gap-2 text-sm font-medium">
              <Key size={14} className="text-muted" />
              {t('consoleKeyTitle')}
            </div>
            <p className="mt-1 text-xs leading-5 text-muted">{t('keysSemanticsConsole')}</p>
          </div>
          <div className="px-4 py-3">
            <div className="flex items-center gap-2 text-sm font-medium">
              <Plugs size={14} className="text-muted" />
              {t('keysProxyTitle')}
            </div>
            <p className="mt-1 text-xs leading-5 text-muted">{t('keysSemanticsProxy')}</p>
          </div>
        </div>
        <p className="border-t border-separator px-4 py-3 text-xs leading-5 text-muted">{t('keysScopeHint')}</p>
      </SectionCard>
    </div>
  )
}
