import { describe, expect, it } from 'vitest'

import { NAV_DOMAINS, NAV_PAGES, NAV_REDIRECTS, navDomainFor, navDomainIsDirect, navDomainLabelKey, navMenuPages, navPageFor } from './nav'

// 结构护栏（重构批次 2；批次 4b 收编接入与观测；批次 8 拆解福利域）：
// 7 域 / 9 页（其中 7 个菜单页）/ 深度恰好两级；「总览 / 账号池 / 任务 / 额度 /
// 接入 / 设置」与「观测」（1 菜单页 + 2 个 menu:false 页内 tab 路径）均 1 击直达。
// 反向注入验证：把任意域的页数/路径改坏，本文件必须点名失败。

describe('nav 单一事实源', () => {
  it('结构：7 域 / 9 页（7 菜单页）/ 深度恰好两级', () => {
    expect(NAV_DOMAINS).toHaveLength(7)
    expect(NAV_PAGES).toHaveLength(9)
    expect(NAV_PAGES.filter((page) => page.menu !== false)).toHaveLength(7)
    for (const page of NAV_PAGES) {
      // 深度 ≤2：/x 或 /x/y（页只挂在域下，不存在三级路径）
      const depth = page.path.split('/').filter(Boolean).length
      expect(depth, `路径 ${page.path} 的深度必须 ≤2`).toBeLessThanOrEqual(2)
      // 每个页恰好属于一个域
      expect(NAV_DOMAINS.filter((domain) => domain.pages.includes(page))).toHaveLength(1)
    }
    // 深度恰好为 2：域下不允许再嵌套（`pages` 是扁平数组，天然保证；
    // 此处断言不存在「页又含子页」的字段泄漏；`menu: false` 为页内 tab 从属路径标记）。
    for (const page of NAV_PAGES) {
      expect(Object.keys(page).filter((key) => key !== 'menu').sort()).toEqual(['descKey', 'key', 'order', 'path'])
    }
  })

  it('路径与 key 全局唯一', () => {
    const paths = NAV_PAGES.map((page) => page.path)
    expect(new Set(paths).size).toBe(paths.length)
    const keys = NAV_PAGES.map((page) => page.key)
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('域序与页序连续且不重复', () => {
    const domainOrders = NAV_DOMAINS.map((domain) => domain.order)
    expect(new Set(domainOrders).size).toBe(domainOrders.length)
    expect([...domainOrders].sort((a, b) => a - b)).toEqual(domainOrders)
    for (const domain of NAV_DOMAINS) {
      const orders = domain.pages.map((page) => page.order)
      expect(new Set(orders).size).toBe(orders.length)
      expect([...orders].sort((a, b) => a - b)).toEqual(orders)
    }
  })

  it('路由解析：精确命中 + 子路径最长前缀 + 未知返回 undefined', () => {
    expect(navPageFor('/')?.key).toBe('navOverview')
    expect(navPageFor('/accounts')?.key).toBe('navAccounts')
    expect(navPageFor('/tasks')?.key).toBe('navTasks')
    expect(navPageFor('/quota')?.key).toBe('navQuota')
    expect(navPageFor('/access')?.key).toBe('navAccess')
    expect(navPageFor('/settings')?.key).toBe('navSettings')
    // 观测：菜单页 + 两个页内 tab 从属路径（menu: false）均参与标题推导
    expect(navPageFor('/logs/requests')?.key).toBe('navObserve')
    expect(navPageFor('/logs/runtime')?.key).toBe('navLogsRuntime')
    expect(navPageFor('/usage')?.key).toBe('navUsage')
    // 详情态子路径归到最长前缀页
    expect(navPageFor('/logs/requests/req-123')?.key).toBe('navObserve')
    // 已迁移路径不再命中独立页（最长前缀归到账号池，仅作标题兜底；跳转由重定向表接管）
    expect(navPageFor('/accounts/expiry')?.key).toBe('navAccounts')
    // 福利页已拆解（批次 8）：/welfare 已无对应页，旧链接由 App 的分流组件接管
    expect(navPageFor('/welfare')).toBeUndefined()
    // 未知路径不匹配任何页
    expect(navPageFor('/models')).toBeUndefined()
    expect(navPageFor('/providers')).toBeUndefined()
    expect(navPageFor('/no-such-page')).toBeUndefined()
  })

  it('域解析与单页域规则（7 域全部 1 击直达 = 扁平 7 项）', () => {
    const direct = NAV_DOMAINS.filter(navDomainIsDirect).map((domain) => domain.key).sort()
    expect(direct).toEqual(['navAccess', 'navDomainAccounts', 'navDomainObserve', 'navDomainOverview', 'navQuota', 'navSettings', 'navTasks'])
    // 单页域：域行标签取菜单页 key（域行 = 页行）
    expect(navDomainLabelKey(NAV_DOMAINS[0])).toBe('navOverview')
    expect(navDomainLabelKey(NAV_DOMAINS[1])).toBe('navAccounts')
    expect(navDomainLabelKey(NAV_DOMAINS[2])).toBe('navTasks')
    expect(navDomainLabelKey(NAV_DOMAINS[3])).toBe('navQuota')
    // 观测：1 菜单页 + 2 个 menu:false 从属路径——域行取菜单页 key（「观测」）
    expect(navDomainLabelKey(NAV_DOMAINS[4])).toBe('navObserve')
    expect(navMenuPages(NAV_DOMAINS[4]).map((page) => page.path)).toEqual(['/logs/requests'])
    expect(navMenuPages(NAV_DOMAINS[4]).length).toBeLessThan(NAV_DOMAINS[4].pages.length)
    // 接入 / 设置：4b / 4a 收编为单页域（域行 = 页行）
    expect(navDomainLabelKey(NAV_DOMAINS[5])).toBe('navAccess')
    expect(navDomainLabelKey(NAV_DOMAINS[6])).toBe('navSettings')
    // 域解析
    expect(navDomainFor('/tasks')?.key).toBe('navTasks')
    expect(navDomainFor('/quota')?.key).toBe('navQuota')
    expect(navDomainFor('/accounts')?.key).toBe('navDomainAccounts')
    expect(navDomainFor('/settings')?.key).toBe('navSettings')
    // 页内 tab 从属路径归属其域（侧栏高亮观测）
    expect(navDomainFor('/logs/runtime')?.key).toBe('navDomainObserve')
    expect(navDomainFor('/usage')?.key).toBe('navDomainObserve')
    // 已拆解的福利页不再归属任何域
    expect(navDomainFor('/welfare')).toBeUndefined()
    expect(navDomainFor('/no-such-page')).toBeUndefined()
  })

  it('兼容重定向清单：旧路径 → 新路径（任务 / 额度 / 设置页签 / 模型目录页签）', () => {
    expect(NAV_REDIRECTS).toEqual([
      { from: '/accounts/expiry', to: '/quota' },
      { from: '/accounts/growth', to: '/tasks' },
      { from: '/providers', to: '/access?tab=models' },
      { from: '/auth', to: '/accounts' },
      { from: '/checkins', to: '/accounts' },
      { from: '/logs', to: '/logs/requests' },
      { from: '/system', to: '/settings' },
      { from: '/system/keys', to: '/settings?tab=keys' },
      { from: '/system/update', to: '/settings?tab=update' },
      { from: '/models', to: '/access?tab=models' },
    ])
    // 重定向目标必须活在菜单页里（防指向已删除的路径；查询串 / 锚点取 pathname 部分比较）。
    const livePaths = new Set(NAV_PAGES.map((page) => page.path))
    for (const redirect of NAV_REDIRECTS) {
      expect(livePaths.has(redirect.to.split(/[?#]/)[0]), `${redirect.from} 的目标 ${redirect.to} 必须存在`).toBe(true)
    }
  })
})
