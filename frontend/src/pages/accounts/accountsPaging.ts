/**
 * 账号池本地分页（列表数据全量在客户端，这里只做切片与边界收敛）。
 * 与 useAccountFilters / expiryModel 等一致的「纯函数 + 单测」模式。
 */

export type PagingResult<T> = {
  /** 当前页的行（末页可不足一页）。 */
  slice: T[]
  /** 收敛后的页码（输入越界 / 非法时钳到 [1, pageCount]）。 */
  page: number
  /** 总页数（空列表也保持 1 页语义，便于 pager 显示）。 */
  pageCount: number
}

export function paginate<T>(items: T[], page: number, pageSize: number): PagingResult<T> {
  const size = Math.max(1, Math.floor(pageSize) || 1)
  const pageCount = Math.max(1, Math.ceil(items.length / size))
  const requested = Math.floor(page) || 1
  const clamped = Math.min(Math.max(1, requested), pageCount)
  const start = (clamped - 1) * size
  return { slice: items.slice(start, start + size), page: clamped, pageCount }
}
