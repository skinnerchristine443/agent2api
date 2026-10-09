import { useRef, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { AppHeader } from './AppHeader'
import { AppSidebar } from './AppSidebar'
import { RouteErrorBoundary } from './RouteErrorBoundary'
import { navPageFor } from '@/nav/nav'
import { useGsapReveal } from '@/hooks/useGsapReveal'
import { useI18n } from '@/hooks/I18nContext'

export function AppLayout() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()
  const { t } = useI18n()
  // 标题与副标题由 nav 单一事实源推导（菜单名 = 页面标题 = 同一 key）。
  // 未知路径（404、redirect 过渡态）回退到概览文案。
  const active = navPageFor(location.pathname)
  const title = t(active?.key ?? 'navOverview')
  const desc = t(active?.descKey ?? 'pageDescOverview')
  const pageRef = useRef<HTMLDivElement>(null)
  useGsapReveal(pageRef, location.pathname)

  return (
    <div className="relative flex h-dvh overflow-hidden bg-background text-foreground">
      <div className="relative z-10 flex min-h-0 min-w-0 flex-1">
        <AppSidebar mobileOpen={mobileOpen} onClose={() => setMobileOpen(false)} />
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
          <AppHeader
            title={title}
            desc={desc}
            onMenu={() => setMobileOpen(true)}
          />
          <main className="min-h-0 flex-1 overflow-y-auto">
            <div className="mx-auto w-full max-w-[1480px] px-4 pb-12 pt-5 sm:px-6 lg:px-8 lg:pb-16 lg:pt-6">
              <div ref={pageRef} key={location.pathname}>
                <RouteErrorBoundary>
                  <Outlet />
                </RouteErrorBoundary>
              </div>
            </div>
          </main>
        </div>
      </div>
    </div>
  )
}
