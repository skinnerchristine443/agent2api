import { describe, expect, it } from 'vitest'

import { buildCurl, maskApiKey, shellQuote } from './accessText'

describe('shellQuote', () => {
  it('单引号包裹并按 POSIX 规则转义内嵌单引号', () => {
    expect(shellQuote('plain')).toBe("'plain'")
    expect(shellQuote("it's")).toBe(`'it'"'"'s'`)
  })
})

describe('buildCurl', () => {
  it('自动路由：不含账号头，默认 Bearer 环境变量', () => {
    const curl = buildCurl({
      endpoint: 'https://gw.example.com/v1/chat/completions',
      payload: { model: 'glm-5.3', stream: false, messages: [{ role: 'user', content: '只回复OK' }] },
    })
    expect(curl).toContain(`curl -sS 'https://gw.example.com/v1/chat/completions' \\`)
    expect(curl).toContain('-H "Authorization: Bearer $AGENT2API_API_KEY"')
    expect(curl).toContain(`-H 'Content-Type: application/json'`)
    expect(curl).not.toContain('X-Agent2API-Account')
    expect(curl).toContain(`-d '{"model":"glm-5.3","stream":false,"messages":[{"role":"user","content":"只回复OK"}]}'`)
  })

  it('钉死账号：追加 X-Agent2API-Account 头（置于 -d 之前）', () => {
    const curl = buildCurl({ endpoint: 'https://gw/v1/chat/completions', payload: { model: 'm' }, accountId: 'acc-1' })
    expect(curl).toContain(`-H 'X-Agent2API-Account: acc-1' \\`)
    expect(curl.indexOf('X-Agent2API-Account')).toBeLessThan(curl.indexOf('-d '))
  })
})

describe('maskApiKey', () => {
  it('保留首尾各 4 位；空串与过短密钥整体隐藏', () => {
    expect(maskApiKey('')).toBe('')
    expect(maskApiKey('short')).toBe('••••')
    expect(maskApiKey('  abcdefghijklmnop  ')).toBe('abcd…mnop')
  })
})
