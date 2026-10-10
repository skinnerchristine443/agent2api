import { Skeleton } from '@heroui/react'

export function SkeletonBlock({ className }: { className: string }) {
  return <Skeleton className={`rounded-lg ${className}`} />
}

export function RankListSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="divide-y divide-separator">
      {Array.from({ length: rows }, (_, index) => (
        <div key={index} className="space-y-2 px-5 py-3">
          <div className="flex items-center justify-between gap-3">
            <SkeletonBlock className="h-4 w-28" />
            <SkeletonBlock className="h-3 w-12" />
          </div>
          <SkeletonBlock className="h-1.5 w-full" />
        </div>
      ))}
    </div>
  )
}

export function ProvidersTableSkeleton({ rows = 6 }: { rows?: number }) {
  return (
    <div>
      <div className="divide-y divide-separator lg:hidden">
        {Array.from({ length: rows }, (_, index) => (
          <div key={`card-${index}`} className="space-y-3 px-4 py-4">
            <div className="flex items-start justify-between gap-3">
              <div className="space-y-2">
                <SkeletonBlock className="h-4 w-32" />
                <SkeletonBlock className="h-3 w-40" />
              </div>
              <SkeletonBlock className="h-5 w-14" />
            </div>
            <SkeletonBlock className="h-3 w-24" />
            <SkeletonBlock className="h-8 w-40" />
            <SkeletonBlock className="h-8 w-24" />
          </div>
        ))}
      </div>
      <div className="hidden lg:block">
        <div className="grid grid-cols-7 gap-4 border-b border-separator px-5 py-4">
          <SkeletonBlock className="h-3 w-20" />
          <SkeletonBlock className="h-3 w-24" />
          <SkeletonBlock className="h-3 w-24" />
          <SkeletonBlock className="h-3 w-20" />
          <SkeletonBlock className="h-3 w-24" />
          <SkeletonBlock className="h-3 w-16" />
          <SkeletonBlock className="h-3 w-16" />
        </div>
        {Array.from({ length: rows }, (_, index) => (
          <div key={`row-${index}`} className="grid grid-cols-7 items-center gap-4 border-b border-separator px-5 py-4 last:border-0">
            <SkeletonBlock className="h-5 w-32" />
            <SkeletonBlock className="h-4 w-28" />
            <SkeletonBlock className="h-4 w-20" />
            <SkeletonBlock className="h-4 w-36" />
            <SkeletonBlock className="h-8 w-40" />
            <SkeletonBlock className="h-5 w-14" />
            <SkeletonBlock className="h-8 w-24" />
          </div>
        ))}
      </div>
    </div>
  )
}

export function OverviewPageSkeleton() {
  return (
    <div className="space-y-5" aria-label="Loading overview">
      {/* 顶部健康条 */}
      <section className="rounded-2xl border border-border bg-surface px-4 py-2.5">
        <div className="flex flex-wrap items-center gap-4">
          <SkeletonBlock className="h-4 w-28" />
          <SkeletonBlock className="h-4 w-28" />
          <SkeletonBlock className="h-4 w-32" />
          <SkeletonBlock className="h-4 w-32" />
          <div className="min-w-4 flex-1" />
          <SkeletonBlock className="h-4 w-56" />
        </div>
      </section>
      {/* 收件箱 + 榜单 */}
      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.65fr)_minmax(0,1fr)]">
        <section className="overflow-hidden rounded-2xl border border-border bg-surface">
          <div className="border-b border-separator px-4 py-3"><SkeletonBlock className="h-4 w-28" /></div>
          {Array.from({ length: 4 }, (_, index) => (
            <div key={index} className="border-t border-separator px-4 py-3 first:border-t-0">
              <SkeletonBlock className="h-4 w-full" />
            </div>
          ))}
        </section>
        <section className="overflow-hidden rounded-2xl border border-border bg-surface">
          <div className="border-b border-separator px-4 py-3"><SkeletonBlock className="h-4 w-32" /></div>
          <div className="space-y-3 p-4">
            {Array.from({ length: 3 }, (_, index) => <SkeletonBlock key={index} className="h-9 w-full" />)}
          </div>
        </section>
      </section>
      {/* 指标卡 */}
      <section className="grid overflow-hidden rounded-2xl border border-border bg-surface sm:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }, (_, index) => (
          <div key={index} className="min-h-32 space-y-5 border-separator p-5 first:border-0 sm:border-l sm:first:border-0">
            <SkeletonBlock className="h-4 w-24" />
            <SkeletonBlock className="h-8 w-28" />
            <SkeletonBlock className="h-3 w-36" />
          </div>
        ))}
      </section>
    </div>
  )
}

export function AccountRowSkeleton() {
  return (
    <div className="flex items-center gap-3 border-b border-separator px-3 py-2.5 last:border-b-0">
      <SkeletonBlock className="size-3.5 rounded-full" />
      <SkeletonBlock className="h-4 w-32" />
      <SkeletonBlock className="h-4 w-16" />
      <SkeletonBlock className="h-2 flex-1" />
      <SkeletonBlock className="h-4 w-24" />
      <SkeletonBlock className="ml-auto h-[18px] w-[74px]" />
    </div>
  )
}

export function AccountsListSkeleton({ count = 8 }: { count?: number }) {
  return (
    <section className="overflow-hidden rounded-xl border border-border bg-surface" aria-label="Loading accounts">
      {Array.from({ length: count }, (_, index) => <AccountRowSkeleton key={index} />)}
    </section>
  )
}

export function AccountsPageSkeleton() {
  return (
    <div className="space-y-4" aria-label="Loading accounts">
      <section className="flex items-end justify-between gap-4 border-b border-separator pb-3">
        <div className="flex flex-wrap gap-5">
          <SkeletonBlock className="h-4 w-20" />
          <SkeletonBlock className="h-4 w-24" />
          <SkeletonBlock className="h-4 w-24" />
          <SkeletonBlock className="h-4 w-20" />
        </div>
        <SkeletonBlock className="h-8 w-28" />
      </section>
      <section className="flex items-center justify-between gap-4">
        <SkeletonBlock className="h-8 w-full max-w-md" />
        <SkeletonBlock className="h-8 w-72" />
      </section>
      <AccountsListSkeleton />
    </div>
  )
}

export function ProvidersPageSkeleton() {
  return (
    <div className="space-y-6" aria-label="Loading models">
      <section className="grid gap-5 border-b border-separator pb-6 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
        <div className="space-y-3">
          <SkeletonBlock className="h-3 w-24" />
          <SkeletonBlock className="h-8 w-44" />
          <SkeletonBlock className="h-4 w-56" />
        </div>
        <div className="flex min-w-0 flex-col gap-2 sm:flex-row">
          <SkeletonBlock className="h-8 w-full sm:w-56" />
          <SkeletonBlock className="h-8 w-24" />
        </div>
      </section>
      <div className="overflow-hidden rounded-2xl border border-border bg-surface">
        <ProvidersTableSkeleton />
      </div>
    </div>
  )
}

export function AccessPageSkeleton() {
  return (
    <div className="space-y-6" aria-label="Loading access settings">
      <section className="grid gap-5 border-b border-separator pb-6 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
        <div className="space-y-3">
          <SkeletonBlock className="h-3 w-32" />
          <SkeletonBlock className="h-8 w-44" />
          <SkeletonBlock className="h-4 w-80 max-w-[70vw]" />
        </div>
        <SkeletonBlock className="h-7 w-24" />
      </section>
      <section className="grid overflow-hidden rounded-2xl border border-border bg-surface sm:grid-cols-3">
        <div className="space-y-3 border-b border-separator p-5 sm:col-span-2 sm:border-r sm:border-b-0">
          <SkeletonBlock className="h-3 w-20" />
          <SkeletonBlock className="h-4 w-72 max-w-full" />
        </div>
        <div className="grid grid-cols-2 gap-4 p-5 sm:grid-cols-1">
          <SkeletonBlock className="h-4 w-24" />
          <SkeletonBlock className="h-4 w-28" />
        </div>
      </section>
      <section className="grid min-h-[620px] overflow-hidden rounded-2xl border border-border bg-surface xl:grid-cols-[minmax(440px,.92fr)_minmax(0,1.08fr)]">
        <div className="space-y-6 border-b border-separator p-7 xl:border-r xl:border-b-0">
          <div className="flex items-center gap-3">
            <SkeletonBlock className="size-9" />
            <div className="space-y-2">
              <SkeletonBlock className="h-4 w-32" />
              <SkeletonBlock className="h-3 w-28" />
            </div>
          </div>
          <div className="grid gap-5 sm:grid-cols-2">
            <div className="space-y-2">
              <SkeletonBlock className="h-4 w-16" />
              <SkeletonBlock className="h-10 w-full" />
            </div>
            <div className="space-y-2">
              <SkeletonBlock className="h-4 w-16" />
              <SkeletonBlock className="h-10 w-full" />
            </div>
          </div>
          <SkeletonBlock className="h-56 w-full" />
          <SkeletonBlock className="h-12 w-full" />
        </div>
        <div className="space-y-4 bg-surface-secondary p-6">
          <div className="flex justify-between">
            <div className="space-y-2">
              <SkeletonBlock className="h-4 w-32" />
              <SkeletonBlock className="h-3 w-44" />
            </div>
            <SkeletonBlock className="h-6 w-20" />
          </div>
          <SkeletonBlock className="mt-10 h-3 w-32" />
          <SkeletonBlock className="h-3 w-full" />
          <SkeletonBlock className="h-3 w-[86%]" />
        </div>
      </section>
      <section className="overflow-hidden rounded-2xl border border-border bg-surface p-5">
        <SkeletonBlock className="h-4 w-36" />
        <SkeletonBlock className="mt-5 h-24 w-full" />
      </section>
    </div>
  )
}

export function LogsRequestListSkeleton() {
  return (
    <div aria-label="Loading logs">
      <div className="hidden grid-cols-10 gap-4 border-b border-separator px-5 py-3 md:grid">
        {Array.from({ length: 10 }, (_, index) => <SkeletonBlock key={index} className="h-3 w-16" />)}
      </div>
      {Array.from({ length: 8 }, (_, index) => (
        <div key={index} className="grid grid-cols-2 items-center gap-4 border-b border-separator px-5 py-3.5 last:border-0 md:grid-cols-10">
          <SkeletonBlock className="h-4 w-28" />
          <SkeletonBlock className="h-4 w-24" />
          <SkeletonBlock className="hidden h-4 w-20 md:block" />
          <SkeletonBlock className="hidden h-4 w-16 md:block" />
          <SkeletonBlock className="hidden h-4 w-16 md:block" />
          <SkeletonBlock className="hidden h-4 w-16 md:block" />
          <SkeletonBlock className="hidden h-4 w-14 md:block" />
          <SkeletonBlock className="hidden h-4 w-12 md:block" />
          <SkeletonBlock className="hidden h-4 w-12 md:block" />
          <SkeletonBlock className="hidden h-4 w-16 md:block" />
        </div>
      ))}
    </div>
  )
}

export function LogsRuntimeListSkeleton() {
  return (
    <div className="divide-y divide-separator" aria-label="Loading logs">
      {Array.from({ length: 8 }, (_, index) => (
        <div key={index} className="grid gap-2 px-5 py-3 sm:grid-cols-[150px_72px_minmax(0,1fr)] sm:items-center">
          <SkeletonBlock className="h-3 w-24" />
          <SkeletonBlock className="h-3 w-12" />
          <SkeletonBlock className="h-4 w-full" />
        </div>
      ))}
    </div>
  )
}
