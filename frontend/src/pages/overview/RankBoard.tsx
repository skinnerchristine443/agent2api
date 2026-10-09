import { useState } from 'react'

import type { RequestStatsBucket, RequestStatsNamed } from '@/api/logs'
import { SectionCard } from '@/components/ui/SectionCard'
import { Segmented } from '@/components/ui/Segmented'
import { RankListSkeleton } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'
import { formatLatency } from '@/lib/format'

import { RankList, type RankItem } from './RankList'
import { errorLabel, familyLabel, statsWindowLabelKey } from './overviewShared'
import type { StatsWindow } from './windowParam'

type RankTab = 'channel' | 'model'

type Props = {
  providers: RequestStatsNamed[]
  models: RequestStatsNamed[]
  errors: RequestStatsBucket[]
  loading: boolean
  hours: StatsWindow
}

/**
 * 工作台榜单卡（设计 D16 / 密度治理 §3.1）：把原「渠道榜 / 模型榜 / 错误构成」
 * 三张卡合并为一张——维度分页（渠道 / 模型）+ 常驻「错误 Top」区块，
 * 纵向压缩；账号榜职责由观测 · 用量统计承接，不再重复。
 */
export function RankBoard({ providers, models, errors, loading, hours }: Props) {
  const { t } = useI18n()
  const [tab, setTab] = useState<RankTab>('channel')

  const providerItems: RankItem[] = (providers || []).map((item) => ({
    key: item.key,
    label: familyLabel(item.key, t),
    count: item.count,
    meta: `${item.ok}/${item.count}`,
    mark: item.key === '(unknown)' ? undefined : item.key,
    to: item.key === '(unknown)' ? undefined : `/accounts?provider=${encodeURIComponent(item.key)}`,
  }))
  const modelItems: RankItem[] = (models || []).map((item) => ({
    key: item.key,
    label: item.key === '(unknown)' ? t('statsUnknown') : item.key,
    count: item.count,
    meta: item.latency_avg_ms != null ? formatLatency(item.latency_avg_ms) : `${item.ok}/${item.count}`,
    to: item.key === '(unknown)' ? undefined : `/logs/requests?model=${encodeURIComponent(item.key)}`,
  }))
  const errorItems: RankItem[] = (errors || []).map((item) => ({
    key: item.key,
    label: errorLabel(item.key, t),
    count: item.count,
    to: `/logs/requests?status=error&kind=${encodeURIComponent(item.key)}`,
  }))

  return (
    <div data-gsap-reveal>
      <SectionCard
        padded={false}
        title={(
          <Segmented
            ariaLabel={t('statsTraffic')}
            value={tab}
            onChange={setTab}
            items={[
              { id: 'channel', label: t('wbRankChannel') },
              { id: 'model', label: t('wbRankModel') },
            ]}
          />
        )}
        right={<span className="text-xs text-muted">{t('wbRankShare')} · {t(statsWindowLabelKey(hours))}</span>}
      >
        {loading ? (
          <RankListSkeleton rows={4} />
        ) : tab === 'channel' ? (
          <RankList items={providerItems} empty={t('statsNoProviders')} />
        ) : (
          <RankList items={modelItems} empty={t('statsNoModels')} />
        )}

        {errors.length > 0 ? (
          <>
            <div className="border-t border-separator px-4 pb-1 pt-3 text-xs font-medium text-muted">
              {t('wbRankErrors')} · {t(statsWindowLabelKey(hours))}
            </div>
            <RankList items={errorItems} empty={t('statsNoErrors')} danger />
          </>
        ) : null}
      </SectionCard>
    </div>
  )
}
