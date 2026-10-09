import { describe, expect, it } from 'vitest'

import { DEFAULT_STATS_WINDOW, parseWindowParam, writeWindowParam } from './windowParam'

function params(query = '') {
  return new URLSearchParams(query)
}

describe('parseWindowParam', () => {
  it('缺省时回退默认窗口', () => {
    expect(parseWindowParam(params())).toBe(DEFAULT_STATS_WINDOW)
    expect(parseWindowParam(params('?q=glm'))).toBe(DEFAULT_STATS_WINDOW)
  })

  it('识别 1h / 24h / 7d', () => {
    expect(parseWindowParam(params('?window=1h'))).toBe(1)
    expect(parseWindowParam(params('?window=24h'))).toBe(24)
    expect(parseWindowParam(params('?window=7d'))).toBe(168)
  })

  it('未知 / 非法取值回退默认窗口', () => {
    expect(parseWindowParam(params('?window=30d'))).toBe(DEFAULT_STATS_WINDOW)
    expect(parseWindowParam(params('?window='))).toBe(DEFAULT_STATS_WINDOW)
  })
})

describe('writeWindowParam', () => {
  it('默认窗口不写入 URL，并清掉已有参数', () => {
    expect(writeWindowParam(params(), DEFAULT_STATS_WINDOW).toString()).toBe('')
    expect(writeWindowParam(params('?window=7d'), DEFAULT_STATS_WINDOW).toString()).toBe('')
  })

  it('非默认窗口写入 window 片段', () => {
    expect(writeWindowParam(params(), 1).toString()).toBe('window=1h')
    expect(writeWindowParam(params(), 168).toString()).toBe('window=7d')
  })

  it('保留其他查询参数（写入与清除均不误伤）', () => {
    expect(writeWindowParam(params('?q=glm&window=1h'), DEFAULT_STATS_WINDOW).toString()).toBe('q=glm')
    expect(writeWindowParam(params('?q=glm'), 1).toString()).toBe('q=glm&window=1h')
  })

  it('写入后可原样解析回窗口（round-trip）', () => {
    expect(parseWindowParam(writeWindowParam(params('?q=x'), 1))).toBe(1)
    expect(parseWindowParam(writeWindowParam(params('?q=x'), 168))).toBe(168)
    expect(parseWindowParam(writeWindowParam(params('?window=1h'), DEFAULT_STATS_WINDOW))).toBe(DEFAULT_STATS_WINDOW)
  })
})
