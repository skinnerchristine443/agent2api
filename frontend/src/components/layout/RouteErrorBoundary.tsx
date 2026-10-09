import { Component, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button } from '@heroui/react'
import { WarningCircle } from '@phosphor-icons/react'
import { useI18n } from '@/hooks/I18nContext'

// 路由级错误边界（重构方案 §10 阶段 1）：渲染异常不再整页白屏；
// 「重试」= 清除错误态重新渲染；仍失败可回概览。
// 由 AppLayout 包裹 Outlet；外层节点带 key={pathname}，切换路由即重新挂载、
// 错误态自动重置。

type Props = { children: ReactNode }
type State = { error: Error | null }

export class RouteErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error) {
    // 仅本地记录，便于排障（个人自用控制台，无远程上报）。
    console.error('[RouteErrorBoundary]', error)
  }

  render() {
    if (!this.state.error) return this.props.children
    return <ErrorFallback error={this.state.error} onRetry={() => this.setState({ error: null })} />
  }
}

function ErrorFallback({ error, onRetry }: { error: Error; onRetry: () => void }) {
  const { t } = useI18n()
  const navigate = useNavigate()
  return (
    <div className="grid min-h-64 place-items-center px-6 py-12 text-center">
      <div className="max-w-md">
        <div className="mb-3 flex justify-center text-danger">
          <WarningCircle size={22} />
        </div>
        <div className="text-sm font-medium">{t('errorBoundaryTitle')}</div>
        <p className="mt-1 text-xs leading-5 text-muted">{t('errorBoundaryDesc')}</p>
        <p className="mono mt-3 break-all rounded-lg border border-separator bg-surface-secondary px-3 py-2 text-left text-micro leading-5 text-muted">
          {error.message || String(error)}
        </p>
        <div className="mt-5 flex justify-center gap-2">
          <Button size="sm" variant="ghost" onPress={onRetry}>
            {t('errorBoundaryRetry')}
          </Button>
          <Button size="sm" onPress={() => navigate('/')}>
            {t('errorBoundaryHome')}
          </Button>
        </div>
      </div>
    </div>
  )
}
