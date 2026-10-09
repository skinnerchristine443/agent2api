import { Button, Skeleton, TextArea } from '@heroui/react'
import { ArrowsClockwise, PaperPlaneTilt } from '@phosphor-icons/react'

import { ProviderMark } from '@/components/brand/ProviderMark'
import { PageAlert } from '@/components/ui/PageAlert'
import { useI18n } from '@/hooks/I18nContext'
import type { AccessData } from '@/hooks/useAccessData'
import { modelCreditsText, modelIsFree } from '@/lib/format'
import { accountProviderLabel } from '@/lib/provider'

import { PlaygroundSelect } from './PlaygroundSelect'

/** 调试台上半区（批次 4b 起纵向堆叠）：账号（自动 / 指定）→ 模型 → 提示词 → 真实调用（POST /api/chat）。 */
export function RequestBuilder({ data, chatPath }: { data: AccessData; chatPath: string }) {
  const { t } = useI18n()
  const selectedAccount = data.accountId
  return (
    <div className="border-b border-separator">
      <div className="border-b border-separator px-5 py-5 sm:px-7">
        <div className="flex items-center gap-3">
          <div className="grid size-8 place-items-center rounded-lg bg-surface-secondary text-foreground">
            <PaperPlaneTilt size={15} weight="bold" />
          </div>
          <div>
            <h3 className="font-semibold tracking-[-0.015em]">{t('requestBuilder')}</h3>
            <p className="mt-0.5 text-xs text-muted">POST {chatPath}</p>
          </div>
        </div>
      </div>

      <div className="space-y-6 p-5 sm:p-7">
        <div className="grid gap-5 sm:grid-cols-2">
          <div className="space-y-2">
            <PlaygroundSelect
              label={t('account')}
              value={selectedAccount || 'auto'}
              onChange={(next) => data.setAccountId(next === 'auto' ? '' : next)}
              options={[
                {
                  id: 'auto',
                  textValue: t('autoAccount'),
                  label: t('autoAccount'),
                  icon: <ArrowsClockwise size={15} className="text-muted" />,
                },
                ...data.accounts.map((account) => ({
                  id: account.id,
                  textValue: `${account.name || account.id} ${accountProviderLabel(account.provider, account.region, t)}`,
                  label: account.name || account.id,
                  hint: accountProviderLabel(account.provider, account.region, t),
                  icon: <ProviderMark provider={account.provider} size={16} />,
                })),
              ]}
            />
            <p className="text-xs leading-5 text-muted">
              {selectedAccount ? t('fixedAccountHint') : t('autoAccountHint')}
            </p>
          </div>

          <div className="space-y-2">
            {data.modelsLoading ? (
              <div className="flex flex-col gap-1">
                <div className="text-sm font-medium text-muted">{t('model')}</div>
                <Skeleton className="h-10 rounded-lg" />
              </div>
            ) : data.models.length ? (
              <PlaygroundSelect
                label={t('model')}
                value={data.selectedModel}
                onChange={data.setModel}
                placeholder={t('model')}
                options={data.models.map((item) => {
                  const credits = modelCreditsText(item)
                  const free = modelIsFree(item)
                  const title = item.display_name || item.id
                  const badge = free ? t('modelFree') : credits
                  const provider = item.provider || item.owned_by || ''
                  return {
                    id: item.id,
                    textValue: `${title} ${item.id} ${provider} ${credits} ${free ? 'free' : ''}`,
                    label: badge ? `${title} ${badge}` : title,
                    hint: [item.id, provider, badge].filter(Boolean).join(' · '),
                  }
                })}
              />
            ) : (
              <div className="flex flex-col gap-1">
                <div className="text-sm font-medium text-muted">{t('model')}</div>
                <div className="flex h-10 items-center rounded-lg border border-dashed border-border px-3 text-xs leading-5 text-muted">
                  {data.modelsError || (selectedAccount ? t('noAccountModels') : t('noModelsYet'))}
                </div>
              </div>
            )}
            <p className="text-xs leading-5 text-muted">
              {selectedAccount ? t('accountModelHint') : t('modelRoutingHint')}
            </p>
          </div>
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between gap-4">
            <div className="text-sm font-medium text-muted">{t('prompt')}</div>
            <span className="mono text-micro text-muted">{data.prompt.length}</span>
          </div>
          <TextArea
            fullWidth
            rows={9}
            className="min-h-56 resize-y px-4 py-3 text-sm leading-6"
            value={data.prompt}
            onChange={(event) => data.setPrompt(event.target.value)}
            placeholder={t('promptPlaceholder')}
            aria-label={t('prompt')}
          />
          <p className="text-xs leading-5 text-muted">{t('promptHelper')}</p>
        </div>

        {!data.accounts.length ? <PageAlert status="warning" title={t('noAccountsForTest')} /> : null}

        <Button
          fullWidth
          isPending={data.requestState === 'loading'}
          isDisabled={!data.prompt.trim() || !data.selectedModel || data.modelsLoading}
          onPress={() => void data.runTest()}
        >
          <PaperPlaneTilt size={16} />
          {data.requestState === 'loading' ? t('requesting') : t('sendRequest')}
        </Button>
      </div>
    </div>
  )
}
