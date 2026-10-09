import { Button } from '@heroui/react'
import { useNavigate } from 'react-router-dom'
import { EmptyPanel } from '@/components/ui/EmptyPanel'
import { useI18n } from '@/hooks/I18nContext'

// 404 页（重构方案 §4.2）：未知路径不再静默重定向到概览，坏链接可见。
// 「返回上一页」在无历史（外部直链进入）时回退概览，避免死按钮。
export function NotFoundPage() {
  const { t } = useI18n()
  const navigate = useNavigate()

  function goBack() {
    if (window.history.length > 1) navigate(-1)
    else navigate('/')
  }

  return (
    <EmptyPanel
      title={t('notFoundTitle')}
      hint={t('notFoundDesc')}
      action={
        <div className="flex gap-2">
          <Button size="sm" variant="ghost" onPress={goBack}>
            {t('notFoundBack')}
          </Button>
          <Button size="sm" onPress={() => navigate('/')}>
            {t('notFoundHome')}
          </Button>
        </div>
      }
    />
  )
}
