import type { Icon } from '@phosphor-icons/react'
import { ChartLine, Cube, Gauge, GearSix, ListChecks, UsersThree, Wallet } from '@phosphor-icons/react'

import type { DictKey } from '@/i18n/messages'

// 导航单一事实源（重构批次 2；批次 4a 收编设置域；批次 8 拆解福利域）：菜单、
// 页面标题、菜单测试全部从本文件读取。结构规则：两级、且仅两级（域 → 页）；
// 单页域的域行即导航链接（1 击直达）。「任务」「额度」「设置」为单页域；
// 观测为 1 菜单页 + 2 个页内 tab 从属路径。**扁平 7 项**。

/** 二级页面（= 菜单项 = 页面标题，同一 i18n key）。 */
export type NavPage = {
  /** 菜单名 = 页面标题（同一 key）。 */
  key: DictKey
  /** 页面副标题（AppHeader 用）。 */
  descKey: DictKey
  /** 路由路径（精确书写，不带尾斜杠）。 */
  path: string
  order: number
  /**
   * `false` = 非菜单页（页内 tab 的从属路径，如观测的运行日志 / 用量）：
   * 仍参与标题推导与域归属，但不进侧栏菜单、不计入「单页域」判定（批次 4b）。
   */
  menu?: false
}

/** 一级域（菜单分组概念，不进 URL 层级）。 */
export type NavDomain = {
  /** 域名 key；单页域的域行展示取该页 key（见 navDomainLabelKey），本 key 留作扩展。 */
  key: DictKey
  icon: Icon
  order: number
  pages: NavPage[]
}

export const NAV_DOMAINS: readonly NavDomain[] = [
  {
    key: 'navDomainOverview',
    icon: Gauge,
    order: 1,
    pages: [{ key: 'navOverview', descKey: 'pageDescOverview', path: '/', order: 1 }],
  },
  {
    key: 'navDomainAccounts',
    icon: UsersThree,
    order: 2,
    pages: [{ key: 'navAccounts', descKey: 'pageDescAccounts', path: '/accounts', order: 1 }],
  },
  {
    // 任务（批次 8：福利域拆解——仅保留成长中心；签到回归账号池、额度独立成域）。
    key: 'navTasks',
    icon: ListChecks,
    order: 3,
    pages: [{ key: 'navTasks', descKey: 'pageDescTasks', path: '/tasks', order: 1 }],
  },
  {
    // 额度（批次 8：到期额度自福利域迁出，独立成域）。
    key: 'navQuota',
    icon: Wallet,
    order: 4,
    pages: [{ key: 'navQuota', descKey: 'pageDescQuota', path: '/quota', order: 1 }],
  },
  {
    // 观测（批次 4b 收编）：单菜单项「观测」+ 页内 3 tab（请求日志 / 运行日志 / 用量）。
    key: 'navDomainObserve',
    icon: ChartLine,
    order: 5,
    pages: [
      { key: 'navObserve', descKey: 'pageDescObserve', path: '/logs/requests', order: 1 },
      { key: 'navLogsRuntime', descKey: 'pageDescLogsRuntime', path: '/logs/runtime', order: 2, menu: false },
      { key: 'navUsage', descKey: 'pageDescUsage', path: '/usage', order: 3, menu: false },
    ],
  },
  {
    // 接入（批次 4b 收编；批次 6 页签化）：单页域——页内三页签（连接信息 / 调试台 / 模型目录）。
    key: 'navAccess',
    icon: Cube,
    order: 6,
    pages: [{ key: 'navAccess', descKey: 'pageDescAccess', path: '/access', order: 1 }],
  },
  {
    // 设置（原「系统」域三页收编）：单页域——页内三页签（运行参数 / 密钥 / 更新）。
    key: 'navSettings',
    icon: GearSix,
    order: 7,
    pages: [{ key: 'navSettings', descKey: 'pageDescSettings', path: '/settings', order: 1 }],
  },
]

/** 全部页面（扁平视图；顺序即「域序 → 页序」，由结构测试守护）。 */
export const NAV_PAGES: readonly NavPage[] = NAV_DOMAINS.flatMap((domain) => domain.pages)

/** 兼容重定向（旧路径 → 新路径；目标可带查询串 / 锚点）。
 *  前六条为兼容表（保留至下一次清理评估，动工时在 docs/03 登记清理条件）。
 *  `/welfare` 带 `?tab=` 的旧链接由 App 的 WelfareLegacyRedirect 按页签分流。 */
export const NAV_REDIRECTS: readonly { from: string; to: string }[] = [
  { from: '/accounts/expiry', to: '/quota' },
  { from: '/accounts/growth', to: '/tasks' },
  { from: '/providers', to: '/access?tab=models' },
  { from: '/auth', to: '/accounts' },
  { from: '/checkins', to: '/accounts' },
  { from: '/logs', to: '/logs/requests' },
  // 设置域收编（批次 4a）：原三页并入 /settings 页签
  { from: '/system', to: '/settings' },
  { from: '/system/keys', to: '/settings?tab=keys' },
  { from: '/system/update', to: '/settings?tab=update' },
  // 接入域收编（批次 4b）：模型目录并入 /access；批次 6 页签化后带 `?tab=models` 直达
  { from: '/models', to: '/access?tab=models' },
]

/** 按 pathname 查页面（精确优先；子路径按最长前缀匹配，供详情态推导标题）。 */
export function navPageFor(pathname: string): NavPage | undefined {
  let best: NavPage | undefined
  for (const page of NAV_PAGES) {
    if (pathname === page.path) return page
    if (page.path !== '/' && pathname.startsWith(`${page.path}/`)) {
      if (!best || page.path.length > best.path.length) best = page
    }
  }
  return best
}

/** 按 pathname 查所属域。 */
export function navDomainFor(pathname: string): NavDomain | undefined {
  const page = navPageFor(pathname)
  return page ? NAV_DOMAINS.find((domain) => domain.pages.includes(page)) : undefined
}

/** 域的菜单页（`menu: false` 的从属路径不进菜单、不计入单页域判定）。 */
export function navMenuPages(domain: NavDomain): readonly NavPage[] {
  return domain.pages.filter((page) => page.menu !== false)
}

/** 域行标签 key：单菜单页域取该页 key（域行 = 页行），多菜单页域取域名 key。 */
export function navDomainLabelKey(domain: NavDomain): DictKey {
  const menuPages = navMenuPages(domain)
  return menuPages.length === 1 ? menuPages[0].key : domain.key
}

/** 单页域：域行即导航链接（点击直达，不弹浮层）。 */
export function navDomainIsDirect(domain: NavDomain): boolean {
  return navMenuPages(domain).length === 1
}
