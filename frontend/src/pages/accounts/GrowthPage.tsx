import { useEffect, useMemo, useState } from 'react'
import { Button } from '@heroui/react'
import { ArrowClockwise } from '@phosphor-icons/react'

import { Banner } from '@/components/ui/Banner'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { PageAlert } from '@/components/ui/PageAlert'
import { PageHeader } from '@/components/ui/PageHeader'
import { SkeletonBlock } from '@/components/ui/skeletons'
import { useGrowthQueries } from '@/hooks/useGrowthQueries'
import { useI18n } from '@/hooks/I18nContext'

import { GrowthAccountPicker } from './GrowthAccountPicker'
import { GrowthClaimSection } from './GrowthClaimSection'
import { GrowthObservations } from './GrowthObservations'
import { GrowthHeatmap, GrowthPanel } from './GrowthPanel'
import { blockErrors } from './growthModel'
import { useGrowthParams } from './useGrowthParams'

function providerOf(provider?: string) {
  return String(provider || '').toLowerCase()
}

/**
 * 成长中心页（账号域）：账号选择 + 分区块容错面板 + 幂等领取（结果分区）+
 * 成长日志。
 *
 * 能力门控（两层）：① **静态过滤**——选择器只列出声明了成长中心接线的渠道
 * （`/api/providers` 的 `capabilities.growth`，由接线一致性测试钉住），
 * 不支持的渠道整个排除、不提供选中；② **动态兜底**——万一实际请求仍返回
 * 400 provider_unsupported，则以显式降级横幅呈现并在本会话置灰。
 */
export function GrowthPage() {
  const { t } = useI18n()
  const { accountId, selectAccount, setDefaultAccount } = useGrowthParams()
  const queries = useGrowthQueries(accountId)
  const [unsupportedProviders, setUnsupportedProviders] = useState<Record<string, boolean>>({})

  const accounts = queries.accounts
  // 静态能力过滤：providers 未就绪时先不过滤（避免静态清单落地前出现假空态），
  // 就绪后仅保留声明 growth 能力的渠道账号。
  const growthAccounts = useMemo(() => {
    if (!queries.providersReady) return accounts
    const capable = new Set(
      queries.providers.filter((provider) => provider.capabilities?.growth === true).map((provider) => provider.id),
    )
    // workbuddy 的国际区域实测没有成长任务领取机制（2026-10-10 生产核查：任务仅
    // 标题、无 code/status/reward，energy=0、travel 空），在渠道能力位之上追加
    // 区域级排除，避免选择器里出现「选了也没有内容」的账号。
    return accounts.filter(
      (account) =>
        capable.has(providerOf(account.provider)) &&
        !(providerOf(account.provider) === 'workbuddy' && account.region === 'global'),
    )
  }, [accounts, queries.providers, queries.providersReady])

  const selected = growthAccounts.find((account) => account.id === accountId) || null
  const selectedProvider = selected ? providerOf(selected.provider) : ''

  // 首帧自动补选：无参数、或参数指向不支持/已不存在的账号时，选中首个
  // 未探明不支持的成长账号（用 replace，不污染历史）。
  useEffect(() => {
    if (!growthAccounts.length) return
    if (accountId && growthAccounts.some((account) => account.id === accountId)) return
    const fallback = growthAccounts.find((account) => !unsupportedProviders[providerOf(account.provider)]) || growthAccounts[0]
    setDefaultAccount(fallback.id)
  }, [accountId, growthAccounts, setDefaultAccount, unsupportedProviders])

  // 探明「该渠道不支持」后记住（本会话），用于选择器置灰与原因说明。
  useEffect(() => {
    if (queries.degrade !== 'unsupported' || !selectedProvider) return
    setUnsupportedProviders((prev) => (prev[selectedProvider] ? prev : { ...prev, [selectedProvider]: true }))
  }, [queries.degrade, selectedProvider])

  // 切换账号（loading）期间立刻以骨架屏取代旧账号内容——旧数据一直挂到
  // 新数据到达会让「切换」看起来像没刷新（批次 7 反馈）。静默刷新（手动
  // 刷新按钮）不置 loading，不受影响。
  const displayStatus = queries.statusLoading ? null : queries.status
  const failures = displayStatus ? blockErrors(displayStatus) : []
  // 面板以「已结算的只读数据」为准：降级（不支持 / 服务未就绪）时没有数据，
  // 只呈现显式降级横幅，不伪造空面板。
  const showSkeleton = Boolean(accountId) && queries.degrade === '' && queries.statusLoading

  return (
    <div className="space-y-5">
      <div data-gsap-reveal>
        <PageHeader
          className="border-b border-separator pb-6"
          description={t('pageDescGrowth')}
          actions={(
            <>
              <GrowthAccountPicker
                accounts={growthAccounts}
                value={accountId}
                unsupportedProviders={unsupportedProviders}
                onChange={selectAccount}
                t={t}
              />
              <Button
                size="sm"
                variant="secondary"
                isDisabled={!accountId}
                isPending={queries.statusLoading}
                onPress={queries.reloadStatus}
              >
                <ArrowClockwise size={15} />{t('refresh')}
              </Button>
            </>
          )}
        />
      </div>

      {!accounts.length && !queries.accountsLoading ? (
        <EmptyPanel
          className="rounded-2xl border border-dashed border-border"
          title={t('growthNoAccounts')}
          hint={t('growthNoAccountsHint')}
        />
      ) : null}

      {queries.providersReady && accounts.length > 0 && growthAccounts.length === 0 ? (
        <EmptyPanel
          className="rounded-2xl border border-dashed border-border"
          title={t('growthNoSupportedAccounts')}
          hint={t('growthNoSupportedHint')}
        />
      ) : null}

      {queries.degrade === 'unsupported' ? (
        <Banner
          status="warning"
          title={t('growthUnsupported')}
          description={t('growthUnsupportedHint')}
          actions={<Button size="sm" variant="ghost" onPress={queries.reloadStatus}>{t('refresh')}</Button>}
        />
      ) : null}

      {queries.degrade === 'unavailable' ? (
        <Banner
          status="warning"
          title={t('growthUnavailable')}
          description={t('growthUnavailableHint')}
          actions={<Button size="sm" variant="ghost" onPress={queries.reloadStatus}>{t('refresh')}</Button>}
        />
      ) : null}

      {queries.statusError ? <PageAlert title={queries.statusError} /> : null}

      {failures.length ? (
        <Banner
          status="danger"
          title={t('growthPartialFailures', { n: failures.length })}
          description={failures.map((failure) => `${t(failure.block)}：${failure.message}`).join('；')}
        />
      ) : null}

      {showSkeleton ? (
        <div className="grid gap-4 xl:grid-cols-2">
          <SkeletonBlock className="h-32 w-full rounded-2xl" />
          <SkeletonBlock className="h-32 w-full rounded-2xl" />
          <SkeletonBlock className="h-32 w-full rounded-2xl" />
          <SkeletonBlock className="h-32 w-full rounded-2xl" />
        </div>
      ) : null}

      {displayStatus ? (
        <>
          <GrowthPanel status={displayStatus} t={t} />

          <GrowthClaimSection
            result={queries.claimResult}
            pending={queries.claimPending}
            error={queries.claimError}
            onClaim={queries.claim}
            t={t}
          />

          <GrowthHeatmap status={displayStatus} t={t} />

          <GrowthObservations
            observations={queries.observations}
            loading={queries.observationsLoading}
            error={queries.observationsError}
            t={t}
          />
        </>
      ) : null}
    </div>
  )
}
