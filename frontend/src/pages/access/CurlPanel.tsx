import { useState } from 'react'
import { Button } from '@heroui/react'
import { Check, Copy, TerminalWindow } from '@phosphor-icons/react'

import { copyText } from '@/lib/clipboard'
import { SectionCard } from '@/components/ui/SectionCard'
import { useI18n } from '@/hooks/I18nContext'

/** curl 示例：与调试台当前编排（端点 / 账号 / 模型 / 提示词）逐字符一致，可一键复制。 */
export function CurlPanel({ curl }: { curl: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)

  async function copy() {
    await copyText(curl)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1100)
  }

  return (
    <SectionCard
      title={(
        <span className="flex min-w-0 items-center gap-2">
          <TerminalWindow className="shrink-0 text-muted" size={16} />
          <span className="truncate">{t('curlExample')}</span>
        </span>
      )}
      hint={t('curlGeneratedHint')}
      right={(
        <Button size="sm" variant="ghost" onPress={() => void copy()}>
          {copied ? <Check size={14} /> : <Copy size={14} />}
          {copied ? t('copied') : t('copy')}
        </Button>
      )}
      padded={false}
    >
      <pre className="mono max-h-80 overflow-auto whitespace-pre-wrap p-5 text-xs leading-6 text-muted">{curl}</pre>
    </SectionCard>
  )
}
