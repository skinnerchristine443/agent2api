// 接入页的纯文本工具：curl 生成（复制用）与密钥掩码。
// 与迁移前 AccessPage 内联实现逐字符等价，便于单测锁住转义行为。

/** POSIX shell 单引号转义。 */
export function shellQuote(value: string) {
  return `'${value.replaceAll("'", `'"'"'`)}'`
}

export type CurlInput = {
  endpoint: string
  payload: unknown
  accountId?: string
}

/** 生成与调试台当前编排一致的 curl 命令（账号钉死时追加 X-Agent2API-Account 头）。 */
export function buildCurl({ endpoint, payload, accountId }: CurlInput) {
  const accountHeader = accountId ? ` \\\n  -H ${shellQuote(`X-Agent2API-Account: ${accountId}`)}` : ''
  return `curl -sS ${shellQuote(endpoint)} \\
  -H "Authorization: Bearer $AGENT2API_API_KEY" \\
  -H ${shellQuote('Content-Type: application/json')}${accountHeader} \\
  -d ${shellQuote(JSON.stringify(payload))}`
}

/** 密钥掩码：仅保留首尾各 4 位；过短（≤8 位）时整体隐藏。 */
export function maskApiKey(key: string) {
  const value = key.trim()
  if (!value) return ''
  if (value.length <= 8) return '••••'
  return `${value.slice(0, 4)}…${value.slice(-4)}`
}
