import { useState } from 'react'
import { Button } from '@heroui/react'
import { Check, Copy, Eye, EyeSlash } from '@phosphor-icons/react'

import type { Translate } from '@/i18n/messages'
import { copyText } from '@/lib/clipboard'

type Props = {
  /** 展示值：未 reveal 时为指纹/占位，reveal 后为完整明文。 */
  value: string
  revealed: boolean
  onReveal: () => void
  onHide: () => void
  t: Translate
}

/**
 * 密钥展示行（批次 7）：指纹/完整值共用一个 mono 框 + 「显示完整密钥 / 隐藏 /
 * 复制」操作。复制仅在 reveal 后出现——指纹不可复制（无意义）。
 */
export function SecretValueRow({ value, revealed, onReveal, onHide, t }: Props) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-center gap-2">
      <code
        className={[
          'mono min-w-0 flex-1 rounded-lg bg-surface-secondary px-3 py-2 text-xs',
          revealed ? 'break-all text-secondary' : 'truncate text-muted',
        ].join(' ')}
      >
        {value}
      </code>
      {revealed ? (
        <>
          <Button
            isIconOnly
            size="sm"
            variant="ghost"
            aria-label={t('copy')}
            onPress={() => {
              void copyText(value)
              setCopied(true)
              window.setTimeout(() => setCopied(false), 1200)
            }}
          >
            {copied ? <Check size={14} className="text-success" /> : <Copy size={14} />}
          </Button>
          <Button isIconOnly size="sm" variant="ghost" aria-label={t('keysHideSecret')} onPress={onHide}>
            <EyeSlash size={14} />
          </Button>
        </>
      ) : (
        <Button size="sm" variant="ghost" onPress={onReveal}>
          <Eye size={14} />{t('keysShowSecret')}
        </Button>
      )}
    </div>
  )
}
