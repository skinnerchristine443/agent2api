import { Suspense, lazy } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useSearchParams } from 'react-router-dom'
import { AppLayout } from '@/components/layout/AppLayout'
import { RequireAuth } from '@/components/layout/RequireAuth'
import { ApiKeyProvider } from '@/hooks/ApiKeyProvider'
import { I18nProvider } from '@/hooks/I18nProvider'
import { OverviewProvider } from '@/hooks/OverviewProvider'
import { NAV_REDIRECTS } from '@/nav/nav'
import { OverviewPage } from '@/pages/overview/OverviewPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { SkeletonBlock } from '@/components/ui/skeletons'

// 路由级代码拆分（react.dev 推荐形态）：首屏只装载入口页，其余页面
// 按需加载——此前的静态导入把 recharts/日志/系统页等全部压进主包
// （1.6MB）。lazy 组件必须声明在模块顶层（避免每次渲染重置状态）。
const AccessPage = lazy(() => import('@/pages/access/AccessPage').then((m) => ({ default: m.AccessPage })))
const AccountsPage = lazy(() => import('@/pages/accounts/AccountsPage').then((m) => ({ default: m.AccountsPage })))
const LoginPage = lazy(() => import('@/pages/LoginPage').then((m) => ({ default: m.LoginPage })))
const SettingsPage = lazy(() => import('@/pages/settings/SettingsPage').then((m) => ({ default: m.SettingsPage })))
const LogsRequestsPage = lazy(() => import('@/pages/logs/LogsRequestsPage').then((m) => ({ default: m.LogsRequestsPage })))
const LogsRuntimePage = lazy(() => import('@/pages/logs/LogsRuntimePage').then((m) => ({ default: m.LogsRuntimePage })))
const UsagePage = lazy(() => import('@/pages/usage/UsagePage').then((m) => ({ default: m.UsagePage })))
const TasksPage = lazy(() => import('@/pages/tasks/TasksPage').then((m) => ({ default: m.TasksPage })))
const QuotaPage = lazy(() => import('@/pages/quota/QuotaPage').then((m) => ({ default: m.QuotaPage })))

// 路由表结构（批次 8：福利域拆解）：7 域 9 页 + 登录/404；
// 页面元数据（标题/路径）以 src/nav/nav.ts 为单一事实源。
// 旧 `/welfare?tab=` 链接由 WelfareLegacyRedirect 按页签分流（见下）。

/**
 * 旧福利页链接分流（批次 8）：`/welfare?tab=` 三页签拆解后，旧链接按页签
 * 转发到新落点（account 参数随行）：growth → /tasks、expiry → /quota、
 * signin（及无 tab）→ /accounts（签到回归账号池，focus 参数随行高亮）。
 */
function WelfareLegacyRedirect() {
  const [params] = useSearchParams()
  const tab = params.get('tab')
  const account = params.get('account')
  const focus = params.get('focus')
  const query = new URLSearchParams()
  if (tab === 'growth' || tab === 'expiry') {
    if (account) query.set('account', account)
    const to = tab === 'growth' ? '/tasks' : '/quota'
    return <Navigate to={query.size ? `${to}?${query}` : to} replace />
  }
  if (focus) query.set('focus', focus)
  return <Navigate to={query.size ? `/accounts?${query}` : '/accounts'} replace />
}

function PageFallback() {
  return (
    <div className="p-6">
      <SkeletonBlock className="h-64 w-full rounded-2xl" />
    </div>
  )
}

export default function App() {
  return (
    <I18nProvider>
      <ApiKeyProvider>
        <OverviewProvider>
          <BrowserRouter>
            <Suspense fallback={<PageFallback />}>
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                <Route element={<RequireAuth />}>
                  <Route element={<AppLayout />}>
                    {/* 总览域 */}
                    <Route path="/" element={<OverviewPage />} />
                    {/* 账号域 */}
                    <Route path="/accounts" element={<AccountsPage />} />
                    {/* 任务域（批次 8：原福利 › 成长中心迁出为独立一级页） */}
                    <Route path="/tasks" element={<TasksPage />} />
                    {/* 额度域（批次 8：原福利 › 到期额度迁出为独立一级页） */}
                    <Route path="/quota" element={<QuotaPage />} />
                    {/* 旧福利页（批次 8 拆解）：按 ?tab= 分流到 /accounts | /tasks | /quota */}
                    <Route path="/welfare" element={<WelfareLegacyRedirect />} />
                    {/* 接入域（批次 4b 收编；批次 6 页签化：连接信息 / 调试台 / 模型目录；旧 /models 经重定向带 ?tab=models 直达） */}
                    <Route path="/access" element={<AccessPage />} />
                    {/* 观测域（页内 3 tab：请求日志 / 运行日志 / 用量统计；/logs 的兼容重定向见 NAV_REDIRECTS） */}
                    <Route path="/logs/requests" element={<LogsRequestsPage />} />
                    <Route path="/logs/runtime" element={<LogsRuntimePage />} />
                    <Route path="/usage" element={<UsagePage />} />
                    {/* 设置域（页内三页签：运行参数 / 密钥 / 更新；旧 /system* 路径经重定向带 tab 直达） */}
                    <Route path="/settings" element={<SettingsPage />} />
                    {/* 兼容重定向与过渡 redirect（清单单一事实源在 nav.ts） */}
                    {NAV_REDIRECTS.map((redirect) => (
                      <Route key={redirect.from} path={redirect.from} element={<Navigate to={redirect.to} replace />} />
                    ))}
                    {/* 404：未知路径不再静默重定向 */}
                    <Route path="*" element={<NotFoundPage />} />
                  </Route>
                </Route>
              </Routes>
            </Suspense>
          </BrowserRouter>
        </OverviewProvider>
      </ApiKeyProvider>
    </I18nProvider>
  )
}
