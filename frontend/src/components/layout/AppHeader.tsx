import { ArrowClockwise, List, SignOut } from '@phosphor-icons/react'
import { Button, Toolbar, Tooltip } from '@heroui/react'
import { useNavigate } from 'react-router-dom'
import { useI18n } from '@/hooks/I18nContext'
import { useOverview } from '@/hooks/OverviewContext'
import { useApiKey } from '@/hooks/ApiKeyContext'

export function AppHeader({
  title,
  desc,
  onMenu,
}: {
  title: string
  desc: string
  onMenu: () => void
}) {
  const { t } = useI18n()
  const { loading, refresh, overview } = useOverview()
  const { signOut } = useApiKey()
  const navigate = useNavigate()
  const ready = Boolean(overview?.worker?.ok)

  return (
    <header className="relative z-20 shrink-0 border-b border-separator bg-background">
      <div className="mx-auto flex w-full max-w-[1480px] items-center gap-4 px-4 py-3 sm:px-6 lg:px-8">
        <Button isIconOnly size="sm" variant="ghost" className="lg:hidden" onPress={onMenu} aria-label={t('menu')}>
          <List size={17} />
        </Button>

        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-3">
            <span className="status-dot translate-y-[-1px]" data-state={ready ? 'ok' : 'danger'} />
            <h1 className="truncate text-2xl font-semibold tracking-[-0.035em]">{title}</h1>
            <p className="hidden truncate text-sm text-muted xl:block">{desc}</p>
          </div>
        </div>

        <Toolbar isAttached>
          <Tooltip>
            <Tooltip.Trigger>
              <Button isIconOnly size="sm" variant="ghost" isPending={loading} onPress={() => void refresh().catch(() => undefined)} aria-label={t('refresh')}>
                <ArrowClockwise size={15} />
              </Button>
            </Tooltip.Trigger>
            <Tooltip.Content>{t('refresh')}</Tooltip.Content>
          </Tooltip>
          <Tooltip>
            <Tooltip.Trigger>
              <Button
                isIconOnly
                size="sm"
                variant="ghost"
                onPress={() => {
                  signOut()
                  navigate('/login', { replace: true })
                }}
                aria-label={t('signOut')}
              >
                <SignOut size={15} />
              </Button>
            </Tooltip.Trigger>
            <Tooltip.Content>{t('signOut')}</Tooltip.Content>
          </Tooltip>
        </Toolbar>
      </div>
    </header>
  )
}
