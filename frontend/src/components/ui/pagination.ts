// 分页常量与类型（与 ListPager 组件分文件：组件文件只导出组件，保持 Fast Refresh 有效）。

export const PAGE_SIZES = [20, 50, 100] as const
export const ACCOUNT_PAGE_SIZES = [20, 50, 100] as const
export type PageSize = (typeof ACCOUNT_PAGE_SIZES)[number]
