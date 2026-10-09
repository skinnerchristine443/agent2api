import { useCallback, useEffect, useMemo } from 'react'
import { Card } from '@heroui/react'
import { useLocation, useSearchParams } from 'react-router-dom'

import { EndpointList } from '@/components/access/EndpointList'
import { PageHeader } from '@/components/ui/PageHeader'
import { Segmented } from '@/components/ui/Segmented'
import { StatusDot } from '@/components/ui/StatusDot'
import { AccessPageSkeleton } from '@/components/ui/skeletons'
import { useApiKey } from '@/hooks/ApiKeyContext'
import { useI18n } from '@/hooks/I18nContext'
import { useOverview } from '@/hooks/OverviewContext'
import { useAccessData } from '@/hooks/useAccessData'
import type { DictKey } from '@/i18n/messages'
import { absUrl } from '@/lib/url'

import { AccessModels } from './AccessModels'
import { ConnectionCard } from './ConnectionCard'
import { CurlPanel } from './CurlPanel'
import { RequestBuilder } from './RequestBuilder'
import { ResponseInspector } from './ResponseInspector'
import { buildCurl } from './accessText'

export type AccessTab = 'connection' | 'playground' | 'models'

const TABS: ReadonlyArray<{ id: AccessTab; labelKey: DictKey }> = [
  { id: 'connection', labelKey: 'accessTabConnection' },
  { id: 'playground', labelKey: 'accessTabPlayground' },
  { id: 'models', labelKey: 'accessTabModels' },
]

function isAccessTab(value: string | null): value is AccessTab {
  return value === 'connection' || value === 'playground' || value === 'models'
}

/**
 * 接入 `/access`（批次 6：页内三页签化——原「两栏 + 端点 + 模型目录下置」
 * 单页过长）。页签状态入 URL（`?tab=`，默认 connection 不写参）；
 * 旧 `/models`、`/providers` 经 NAV_REDIRECTS 带 `?tab=models` 直达；
 * 批次 4b 的 `#models` 锚点深链在挂载时改写为页签参数（旧书签不迷路）。
 *
 * 页面标题由 AppHeader（nav 单一事实源）承担；页内不再渲染页面级 h2。
 */
export function AccessPage() {
  const { t } = useI18n()
  const { overview, loading } = useOverview()
  const { apiKey } = useApiKey()
  const data = useAccessData()
  const location = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()
  const raw = searchParams.get('tab')
  const tab: AccessTab = isAccessTab(raw) ? raw : 'connection'

  const setTab = useCallback((next: AccessTab) => {
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      if (next === 'connection') params.delete('tab')
      else params.set('tab', next)
      return params
    }, { replace: true })
  }, [setSearchParams])

  // 历史锚点兼容：`/access#models`（批次 4b 深链）→ `?tab=models`。
  useEffect(() => {
    if (location.hash !== '#models') return
    setSearchParams((prev) => {
      const params = new URLSearchParams(prev)
      params.set('tab', 'models')
      return params
    }, { replace: true })
  }, [location.hash, setSearchParams])

  const base = absUrl(overview?.access?.openai_base_url || '/v1')
  const chatPath = overview?.access?.chat_completions || '/v1/chat/completions'
  const chatEndpoint = absUrl(chatPath)
  const curl = useMemo(
    () => buildCurl({ endpoint: chatEndpoint, payload: data.payload, accountId: data.accountId || undefined }),
    [chatEndpoint, data.payload, data.accountId],
  )

  if (loading && !overview) return <AccessPageSkeleton />

  return (
    <div className="space-y-5">
      <div data-gsap-reveal>
        <PageHeader
          className="border-b border-separator pb-6"
          description={t('apiPlaygroundHint')}
          actions={(
            <div className="flex items-center gap-2 text-xs text-muted">
              <StatusDot state={data.readyAccounts.length ? 'ok' : 'warn'} />
              <span>{t('readyAccounts', { ready: data.readyAccounts.length, total: data.accounts.length })}</span>
            </div>
          )}
        />
      </div>

      <div data-gsap-reveal>
        <Segmented
          ariaLabel={t('navAccess')}
          value={tab}
          onChange={setTab}
          items={TABS.map((item) => ({ id: item.id, label: t(item.labelKey) }))}
        />
      </div>

      {tab === 'connection' ? (
        <div className="grid items-start gap-5 xl:grid-cols-[minmax(300px,0.8fr)_minmax(0,1.55fr)]">
          <div data-gsap-reveal>
            <ConnectionCard base={base} apiKey={apiKey} ready={data.readyAccounts.length > 0} />
          </div>
          <div data-gsap-reveal>
            <EndpointList access={overview?.access} />
          </div>
        </div>
      ) : null}

      {tab === 'playground' ? (
        <div className="space-y-5">
          <div data-gsap-reveal>
            <Card className="overflow-hidden p-0">
              <RequestBuilder data={data} chatPath={chatPath} />
              <ResponseInspector data={data} />
            </Card>
          </div>
          <div data-gsap-reveal>
            <CurlPanel curl={curl} />
          </div>
        </div>
      ) : null}

      {tab === 'models' ? <AccessModels /> : null}
    </div>
  )
}
