import type { ChangeEvent, RefObject } from 'react'
import { Button, TextArea } from '@heroui/react'
import { CheckCircle, FileCode } from '@phosphor-icons/react'

import type { ImportBatchItem } from '@/api/overview'
import type { DictKey, Translate } from '@/i18n/messages'

type Props = {
  isDone: boolean
  busy: boolean
  message: string
  json: string
  batchRows: ImportBatchItem[]
  onJsonChange: (value: string) => void
  onPickFile: () => void
  onFileChange: (event: ChangeEvent<HTMLInputElement>) => void
  fileInputRef: RefObject<HTMLInputElement | null>
  onSubmit: () => void
  t: Translate
}

/** batchStatusKey 将导入批次某一行的 status 映射到其 i18n 标签。 */
function batchStatusKey(status: string): DictKey {
  if (status === 'imported') return 'importStatusImported'
  if (status === 'skipped') return 'importStatusSkipped'
  return 'importStatusError'
}

/** 凭证导入页签：单个凭证包 JSON / 数组批量（逐项报告）。 */
export function AddAccountImportTab({
  isDone,
  busy,
  message,
  json,
  batchRows,
  onJsonChange,
  onPickFile,
  onFileChange,
  fileInputRef,
  onSubmit,
  t,
}: Props) {
  return (
    <>
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-medium text-muted">JSON</span>
        <Button size="sm" variant="secondary" onPress={onPickFile} isDisabled={busy}>
          <FileCode size={13} />{t('wizardChooseFile')}
        </Button>
        <input ref={fileInputRef} type="file" accept="application/json,.json" className="hidden" onChange={onFileChange} />
      </div>
      <TextArea
        className="min-h-32 font-mono text-xs"
        value={json}
        onChange={(event) => onJsonChange(event.target.value)}
        placeholder={t('wizardImportPh')}
        aria-label={t('tabImport')}
        disabled={busy}
      />
      {message ? (
        <p className="flex items-center gap-2 rounded-lg border border-separator bg-surface-secondary px-3 py-2 text-xs">
          {isDone ? <CheckCircle size={14} className="shrink-0 text-success" /> : null}
          <span className={isDone ? 'font-medium text-foreground' : 'text-muted'}>{message}</span>
        </p>
      ) : null}
      {batchRows.length > 0 ? (
        <ul
          className="max-h-40 space-y-1 overflow-y-auto rounded-lg border border-separator bg-surface-secondary px-3 py-2 text-xs"
          aria-label={t('tabImport')}
        >
          {batchRows.map((row) => (
            <li key={row.index} className="flex items-start gap-2">
              <span className={`shrink-0 font-medium ${row.status === 'imported' ? 'text-success' : row.status === 'skipped' ? 'text-muted' : 'text-danger'}`}>
                {t(batchStatusKey(row.status))}
              </span>
              <span className="min-w-0 break-all text-muted">
                {row.name || `#${row.index + 1}`}{row.error ? ` — ${row.error}` : ''}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
      <Button className="w-full" isPending={busy} onPress={onSubmit}>
        {isDone
          ? <><CheckCircle size={15} />{t('accountImported')}</>
          : <><FileCode size={15} />{t('importCredential')}</>}
      </Button>
    </>
  )
}
