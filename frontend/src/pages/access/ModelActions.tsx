import { Button } from '@heroui/react'
import { Info } from '@phosphor-icons/react'

import type { ModelInfo } from '@/api/types'
import { useI18n } from '@/hooks/I18nContext'

/** 行内操作：打开模型详情模态。 */
export function ModelActions({
  model,
  onDetails,
}: {
  model: ModelInfo
  onDetails: (model: ModelInfo) => void
}) {
  const { t } = useI18n()
  return (
    <Button size="sm" variant="ghost" onPress={() => onDetails(model)}>
      <Info size={14} />{t('modelDetails')}
    </Button>
  )
}
