import { useEffect, useRef, useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { Drawer, Button, Chip, Tooltip } from '@heroui/react'
import { CaretDown, SidebarSimple, X } from '@phosphor-icons/react'
import { BrandMark } from '@/components/brand/BrandMark'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { gsap } from 'gsap'
import { pressScale } from '@/hooks/useGsapReveal'
import { useI18n } from '@/hooks/I18nContext'
import { useOverview } from '@/hooks/OverviewContext'
import { NAV_DOMAINS, navDomainFor, navDomainIsDirect, navDomainLabelKey, navMenuPages, type NavDomain } from '@/nav/nav'

// 两级导航（重构方案终版 §5）：一级 = 域，二级 = 页面（结构单一事实源在
// src/nav/nav.ts）。展开态：域行可折叠 + 缩进二级项；单页域（总览）域行
// 即导航链接、不弹浮层。折叠态：5 个域图标，点击弹 flyout 列出该域页面。

const COLLAPSE_KEY = 'agent2api:sidebar-collapsed'
const OPEN_DOMAINS_KEY = 'agent2api:nav-open-domains'

// 域展开状态持久化（侧栏 UI 偏好，与折叠态同层，直接使用 localStorage）。
function readOpenDomains(): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(OPEN_DOMAINS_KEY) || '[]')
    return Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === 'string') : []
  } catch {
    return []
  }
}

function writeOpenDomains(keys: string[]) {
  localStorage.setItem(OPEN_DOMAINS_KEY, JSON.stringify(keys))
}

function navRowClass(active: boolean) {
  return `group/navitem relative flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-[14px] transition-colors will-change-transform ${
    active ? 'bg-surface-secondary font-semibold text-foreground' : 'font-medium text-muted hover:bg-surface-secondary hover:text-foreground'
  }`
}

function subItemClass(active: boolean) {
  return `relative flex items-center rounded-lg px-3 py-1.5 text-caption transition-colors ${
    active ? 'bg-surface-secondary font-semibold text-foreground' : 'font-medium text-muted hover:bg-surface-secondary hover:text-foreground'
  }`
}

type Props = {
  mobileOpen: boolean
  onClose: () => void
}

function NavList({ collapsed, onNavigate }: { collapsed: boolean; onNavigate?: () => void }) {
  const { t } = useI18n()
  const location = useLocation()
  const navRef = useRef<HTMLElement>(null)
  const indicatorRef = useRef<HTMLSpanElement>(null)
  const [openDomains, setOpenDomains] = useState<string[]>(() => readOpenDomains())
  const [flyoutDomain, setFlyoutDomain] = useState<string | null>(null)
  const activeDomain = navDomainFor(location.pathname)

  // 当前域自动展开（仅运行时补充，不覆盖持久化偏好）。
  useEffect(() => {
    const domain = navDomainFor(location.pathname)
    if (!domain || navDomainIsDirect(domain)) return
    setOpenDomains((current) => (current.includes(domain.key) ? current : [...current, domain.key]))
  }, [location.pathname])

  // 折叠态 flyout：Esc 或点击导航外关闭。
  useEffect(() => {
    if (!flyoutDomain) return
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') setFlyoutDomain(null)
    }
    function onPointerDown(event: MouseEvent) {
      if (navRef.current && !navRef.current.contains(event.target as Node)) setFlyoutDomain(null)
    }
    document.addEventListener('keydown', onKeyDown)
    document.addEventListener('mousedown', onPointerDown)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.removeEventListener('mousedown', onPointerDown)
    }
  }, [flyoutDomain])

  // 左侧指示条（保留原 GSAP 竖条）：跟随当前页；折叠态隐藏。
  useEffect(() => {
    const navElement = navRef.current
    const indicator = indicatorRef.current
    if (!navElement || !indicator) return
    if (collapsed) {
      gsap.set(indicator, { autoAlpha: 0 })
      return
    }
    const active = navElement.querySelector<HTMLElement>('[aria-current="page"]')
    if (!active) {
      gsap.set(indicator, { autoAlpha: 0 })
      return
    }
    const navRect = navElement.getBoundingClientRect()
    const activeRect = active.getBoundingClientRect()
    gsap.to(indicator, {
      x: activeRect.left - navRect.left,
      y: activeRect.top - navRect.top + activeRect.height / 2 - 8,
      autoAlpha: 1,
      scaleY: 1,
      duration: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 0.28,
      ease: 'power3.out',
      overwrite: true,
    })
    return () => gsap.killTweensOf(indicator)
  }, [collapsed, location.pathname, openDomains])

  function toggleDomain(domain: NavDomain) {
    setOpenDomains((current) => {
      const next = current.includes(domain.key) ? current.filter((key) => key !== domain.key) : [...current, domain.key]
      writeOpenDomains(next)
      return next
    })
  }

  return (
    <nav ref={navRef} className={collapsed ? 'relative flex flex-col items-center gap-1.5' : 'relative flex flex-col gap-3'}>
      <span ref={indicatorRef} aria-hidden className="pointer-events-none absolute left-0 top-0 h-4 w-0.5 origin-center rounded-full bg-accent opacity-0" />
      {NAV_DOMAINS.map((domain) => {
        const label = t(navDomainLabelKey(domain))
        const direct = navDomainIsDirect(domain)
        const menuPages = navMenuPages(domain)
        const DomainIcon = domain.icon
        const activeHere = activeDomain?.key === domain.key

        if (collapsed) {
          if (direct) {
            return (
              <NavLink
                key={domain.key}
                to={menuPages[0].path}
                end
                onClick={onNavigate}
                aria-label={label}
                title={label}
                className={({ isActive }) =>
                  `flex h-10 w-10 items-center justify-center rounded-xl transition-colors ${
                    isActive ? 'bg-surface-secondary text-foreground' : 'text-muted hover:bg-surface-secondary hover:text-foreground'
                  }`
                }
                onMouseDown={(event) => pressScale(event.currentTarget)}
              >
                <span className="inline-flex shrink-0" data-nav-icon><DomainIcon size={18} weight={activeHere ? 'fill' : 'regular'} /></span>
              </NavLink>
            )
          }
          const flyoutOpen = flyoutDomain === domain.key
          return (
            <div key={domain.key} className="relative">
              <button
                type="button"
                aria-haspopup="menu"
                aria-expanded={flyoutOpen}
                aria-label={label}
                title={label}
                onClick={() => setFlyoutDomain(flyoutOpen ? null : domain.key)}
                className={`flex h-10 w-10 items-center justify-center rounded-xl transition-colors ${
                  activeHere || flyoutOpen ? 'bg-surface-secondary text-foreground' : 'text-muted hover:bg-surface-secondary hover:text-foreground'
                }`}
                onMouseDown={(event) => pressScale(event.currentTarget)}
              >
                <span className="inline-flex shrink-0" data-nav-icon><DomainIcon size={18} weight={activeHere ? 'fill' : 'regular'} /></span>
              </button>
              {flyoutOpen && (
                <div role="menu" className="absolute left-full top-1/2 z-50 ml-2 min-w-40 -translate-y-1/2 rounded-xl border border-border bg-surface p-1.5 shadow-overlay">
                  <div className="px-3 py-1.5 text-micro font-semibold uppercase tracking-[0.12em] text-muted">{label}</div>
                  {menuPages.map((page) => (
                    <NavLink
                      key={page.key}
                      to={page.path}
                      end
                      className={({ isActive }) =>
                        `block rounded-lg px-3 py-2 text-caption transition-colors ${
                          isActive ? 'bg-surface-secondary font-semibold text-foreground' : 'font-medium text-muted hover:bg-surface-secondary hover:text-foreground'
                        }`
                      }
                    >
                      {t(page.key)}
                    </NavLink>
                  ))}
                </div>
              )}
            </div>
          )
        }

        const expanded = direct || openDomains.includes(domain.key)
        return (
          <div key={domain.key} className="flex flex-col gap-0.5">
            {direct ? (
              <NavLink
                to={menuPages[0].path}
                end
                onClick={onNavigate}
                aria-label={label}
                className={({ isActive }) => navRowClass(isActive)}
                onMouseDown={(event) => pressScale(event.currentTarget)}
              >
                <span className="inline-flex shrink-0" data-nav-icon><DomainIcon size={18} weight={activeHere ? 'fill' : 'regular'} /></span>
                <span className="truncate">{label}</span>
              </NavLink>
            ) : (
              <button
                type="button"
                aria-expanded={expanded}
                onClick={() => toggleDomain(domain)}
                className={navRowClass(activeHere)}
                onMouseDown={(event) => pressScale(event.currentTarget)}
              >
                <span className="inline-flex shrink-0" data-nav-icon><DomainIcon size={18} weight={activeHere ? 'fill' : 'regular'} /></span>
                <span className="truncate">{label}</span>
                <CaretDown
                  size={12}
                  className={`ml-auto shrink-0 text-muted transition-transform duration-[240ms] ease-out motion-reduce:transition-none ${expanded ? '' : '-rotate-90'}`}
                />
              </button>
            )}
            {!direct && (
              <div
                className={`ml-3 flex flex-col gap-0.5 overflow-hidden transition-all duration-[240ms] ease-out motion-reduce:transition-none ${
                  expanded ? 'max-h-64 opacity-100' : 'max-h-0 opacity-0'
                }`}
              >
                {menuPages.map((page) => (
                  <NavLink
                    key={page.key}
                    to={page.path}
                    end
                    onClick={onNavigate}
                    className={({ isActive }) => subItemClass(isActive)}
                    onMouseDown={(event) => pressScale(event.currentTarget)}
                  >
                    {t(page.key)}
                  </NavLink>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </nav>
  )
}

function Brand({ compact = false }: { compact?: boolean }) {
  const { t } = useI18n()
  return (
    <div className={`flex items-center gap-3 ${compact ? 'justify-center' : 'px-2'}`}>
      <BrandMark size={32} />
      {!compact && <div><div className="text-[14px] font-semibold tracking-[-0.015em]">agent2api</div><div className="text-micro text-muted">{t('controlPlane')}</div></div>}
    </div>
  )
}

function SidebarStatusSkeleton({ label }: { label: string }) {
  return (
    <div className="rounded-xl border border-border bg-surface p-3" aria-busy="true" aria-label={label}>
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <SkeletonBlock className="size-2" />
          <SkeletonBlock className="h-4 w-12" />
        </div>
        <SkeletonBlock className="h-5 w-10" />
      </div>
      <div className="mt-3 grid grid-cols-2 gap-3 border-t border-separator pt-3">
        <div>
          <SkeletonBlock className="h-3 w-10" />
          <SkeletonBlock className="mt-1.5 h-4 w-12" />
        </div>
        <div>
          <SkeletonBlock className="h-3 w-8" />
          <SkeletonBlock className="mt-1.5 h-4 w-10" />
        </div>
      </div>
    </div>
  )
}

export function AppSidebar({ mobileOpen, onClose }: Props) {
  const { t } = useI18n()
  const { overview, loading } = useOverview()
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSE_KEY) === '1')
  const proxyOk = Boolean(overview?.proxy?.ok)
  const workerOk = Boolean(overview?.worker?.ok)
  const healthy = proxyOk && workerOk
  const accountCount = overview?.worker?.account_count ?? 0
  const hotCount = overview?.worker?.hot_count ?? 0
  const showStatusSkeleton = loading

  function toggleCollapsed() {
    setCollapsed((current) => {
      const next = !current
      localStorage.setItem(COLLAPSE_KEY, next ? '1' : '0')
      return next
    })
  }

  const footer = (
    <div className="mt-auto shrink-0 space-y-3 border-t border-separator pt-4">
      {!collapsed && (
        showStatusSkeleton ? (
          <SidebarStatusSkeleton label={t('refreshing')} />
        ) : (
          <div className="rounded-xl border border-border bg-surface p-3">
            <div className="flex items-center justify-between gap-3">
              <div className="flex items-center gap-2 text-sm font-medium">
                <span className="status-dot" data-state={healthy ? 'ok' : 'danger'} />
                {healthy ? t('running') : t('degraded')}
              </div>
              <Chip size="sm" variant="soft" color={healthy ? 'success' : 'warning'}>{hotCount}/{accountCount}</Chip>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-3 border-t border-separator pt-3 text-xs">
              <div>
                <div className="text-muted">Proxy</div>
                <div className="mt-1 font-medium">{proxyOk ? 'online' : 'down'}</div>
              </div>
              <div>
                <div className="text-muted">{t('accountCount')}</div>
                <div className="mono mt-1 font-medium">{hotCount}/{accountCount}</div>
              </div>
            </div>
          </div>
        )
      )}
      <div className={`flex items-center gap-2 ${collapsed ? 'flex-col' : ''}`}>
        <Tooltip>
          <Tooltip.Trigger>
            <Button isIconOnly size="sm" variant="ghost" className={collapsed ? '' : 'hidden'} aria-label={t('runtimeSnapshot')}>
              {showStatusSkeleton ? <SkeletonBlock className="size-2" /> : <span className="status-dot" data-state={healthy ? 'ok' : 'danger'} />}
            </Button>
          </Tooltip.Trigger>
          <Tooltip.Content>{showStatusSkeleton ? t('refreshing') : healthy ? t('running') : t('degraded')}</Tooltip.Content>
        </Tooltip>
        <Button isIconOnly size="sm" variant="ghost" className={collapsed ? '' : 'ml-auto'} onPress={toggleCollapsed} aria-label={t('toggleSidebar')}><SidebarSimple size={16} className={collapsed ? 'rotate-180' : ''} /></Button>
      </div>
      {!collapsed && <p className="px-1 text-micro leading-5 text-muted">{t('sidebarFoot')}</p>}
    </div>
  )

  return (
    <>
      <aside className={`hidden h-dvh shrink-0 flex-col border-r border-separator bg-surface-secondary text-foreground transition-[width] duration-300 ease-out md:flex ${collapsed ? 'w-[76px] px-2.5 py-4' : 'w-[248px] px-3 py-4'}`}>
        <div className={`flex h-9 shrink-0 items-center ${collapsed ? 'justify-center' : 'justify-start'}`}><Brand compact={collapsed} /></div>
        {/* 折叠态关闭纵向滚动，避免 absolute flyout 被 overflow 裁剪。 */}
        <div className={`mt-5 min-h-0 flex-1 ${collapsed ? 'overflow-visible' : 'overflow-y-auto overscroll-contain'}`}><NavList collapsed={collapsed} /></div>
        {footer}
      </aside>
      <Drawer isOpen={mobileOpen} onOpenChange={(open) => { if (!open) onClose() }}>
        <Drawer.Content placement="left">
          <Drawer.Dialog className="w-72 bg-surface-secondary p-3 text-foreground">
            <div className="flex items-center justify-between px-2 py-3"><Brand /><Button isIconOnly size="sm" variant="ghost" onPress={onClose} aria-label={t('close')}><X size={16} /></Button></div>
            <Drawer.Body className="px-0"><NavList collapsed={false} onNavigate={onClose} /></Drawer.Body>
          </Drawer.Dialog>
        </Drawer.Content>
      </Drawer>
    </>
  )
}
