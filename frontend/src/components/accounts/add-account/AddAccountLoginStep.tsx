import { Button } from '@heroui/react'
import { CaretLeft, FileCode, Key, ShieldCheck } from '@phosphor-icons/react'

import { ProviderMark } from '@/components/brand/ProviderMark'
import { FilterToggle, type FilterToggleOption } from '@/components/ui/FilterToggle'
import type { Translate } from '@/i18n/messages'

import { AddAccountBrowserTab } from './AddAccountBrowserTab'
import { AddAccountImportTab } from './AddAccountImportTab'
import { AddAccountPatTab } from './AddAccountPatTab'
import { AddAccountSettings } from './AddAccountSettings'
import { optionLabel } from './providerOptions'
import type { AddAccountTab } from './types'
import type { AddAccountWizard } from './useAddAccountWizard'

type Props = {
  wizard: AddAccountWizard
  t: Translate
}

/** 添加向导第二步：账号设置 + 登录方式页签（浏览器 / PAT / 导入）。 */
export function AddAccountLoginStep({ wizard, t }: Props) {
  const activeOption = wizard.activeOption
  const methodOptions = [
    wizard.hasBrowserLogin ? { id: 'browser', label: t('tabBrowser'), icon: <ShieldCheck size={16} /> } : null,
    wizard.showPatTab ? { id: 'pat', label: t('tabPat'), icon: <Key size={16} /> } : null,
    wizard.showImportTab ? { id: 'import', label: t('tabImport'), icon: <FileCode size={16} /> } : null,
  ].filter(Boolean) as FilterToggleOption[]

  const tabLead = wizard.tab === 'browser'
    ? t('wizardBrowserLead')
    : wizard.tab === 'pat'
      ? t('wizardPatLead')
      : t('wizardImportLead')

  return (
    <>
      <div className="flex items-center gap-3 rounded-lg border border-separator bg-surface-secondary/45 px-3 py-2.5">
        <span className="shrink-0"><ProviderMark provider={activeOption?.provider} size={18} /></span>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium text-foreground">
            {activeOption ? optionLabel(activeOption, t) : t('accountType')}
          </div>
          <div className="truncate text-micro text-muted">{wizard.hint}</div>
        </div>
        <Button
          size="sm"
          variant="ghost"
          onPress={wizard.backToMethod}
          isDisabled={wizard.settingsLocked}
        >
          <CaretLeft size={12} />{t('wizardBack')}
        </Button>
      </div>

      <AddAccountSettings
        name={wizard.name}
        onNameChange={wizard.setName}
        maxInFlight={wizard.maxInFlight}
        onMaxInFlightChange={wizard.setMaxInFlight}
        priority={wizard.priority}
        onPriorityChange={wizard.setPriority}
        proxyUrl={wizard.proxyUrl}
        onProxyUrlChange={wizard.setProxyUrl}
        dropSystemPrompt={wizard.dropSystemPrompt}
        onDropSystemPromptChange={wizard.setDropSystemPrompt}
        showDropSystem={wizard.showDropSystem}
        locked={wizard.settingsLocked}
        advancedOpen={wizard.advancedOpen}
        onToggleAdvanced={() => wizard.setAdvancedOpen(!wizard.advancedOpen)}
        t={t}
      />

      <FilterToggle
        className="mt-3"
        value={wizard.tab}
        onChange={(next) => wizard.switchTab(next as AddAccountTab)}
        ariaLabel={t('authentication')}
        options={methodOptions}
      />

      <p className="mt-2 min-h-5 text-xs leading-5 text-muted">{tabLead}</p>

      <div className="mt-3 flex flex-col gap-4">
        {wizard.tab === 'browser' ? (
          <AddAccountBrowserTab
            phase={wizard.phase}
            busy={wizard.busy}
            isDone={wizard.isDone}
            message={wizard.message}
            authUrl={wizard.authUrl}
            callbackUrl={wizard.callbackUrl}
            showCallbackPaste={wizard.showCallbackPaste}
            onCallbackChange={wizard.setCallbackUrl}
            onSubmitCallback={wizard.submitCallback}
            onStart={wizard.startBrowser}
            t={t}
          />
        ) : wizard.tab === 'pat' ? (
          <AddAccountPatTab
            isDone={wizard.isDone}
            busy={wizard.busy}
            message={wizard.message}
            pat={wizard.pat}
            onPatChange={wizard.setPat}
            onSubmit={wizard.submitPat}
            t={t}
          />
        ) : (
          <AddAccountImportTab
            isDone={wizard.isDone}
            busy={wizard.busy}
            message={wizard.message}
            json={wizard.json}
            batchRows={wizard.batchRows}
            onJsonChange={wizard.setJson}
            onPickFile={wizard.onPickFile}
            onFileChange={wizard.onFileChange}
            fileInputRef={wizard.fileInput}
            onSubmit={wizard.submitImportFromJson}
            t={t}
          />
        )}
      </div>
    </>
  )
}
