import { describe, expect, it } from 'vitest'

import { paginate } from './accountsPaging'

const items = (n: number) => Array.from({ length: n }, (_, i) => i + 1)

describe('paginate', () => {
  it('空列表：保持 1 页语义且切出空行', () => {
    const r = paginate([], 1, 20)
    expect(r.slice).toEqual([])
    expect(r.page).toBe(1)
    expect(r.pageCount).toBe(1)
  })

  it('不足一页：全部返回、单页', () => {
    const r = paginate(items(10), 1, 20)
    expect(r.slice).toEqual(items(10))
    expect(r.pageCount).toBe(1)
  })

  it('正好整除：末页完整', () => {
    const r = paginate(items(40), 2, 20)
    expect(r.pageCount).toBe(2)
    expect(r.slice).toEqual(items(40).slice(20)) // 21..40
  })

  it('末页不足一页：只切出剩余行', () => {
    const r = paginate(items(21), 2, 20)
    expect(r.pageCount).toBe(2)
    expect(r.slice).toEqual([21])
  })

  it('页码越界（大于总页数）：收敛到末页', () => {
    const r = paginate(items(21), 99, 20)
    expect(r.page).toBe(2)
    expect(r.slice).toEqual([21])
  })

  it('页码非法（NaN / 0 / 负数）：回退首页', () => {
    expect(paginate(items(21), Number.NaN, 20).page).toBe(1)
    expect(paginate(items(21), 0, 20).page).toBe(1)
    expect(paginate(items(21), -3, 20).page).toBe(1)
  })

  it('每页长变化：同一页码重新切片（由调用方在变化时复位到第 1 页）', () => {
    const r = paginate(items(45), 1, 50)
    expect(r.pageCount).toBe(1)
    expect(r.slice).toEqual(items(45))
  })
})
