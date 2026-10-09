#!/usr/bin/env node
/**
 * 前端约定护栏（T51-C 交付；方案 §6.2 / §6.5 / §8.3-8）
 *
 * 用法：
 *   node scripts/check-conventions.mjs            # 基线模式：仅「新增违规」exit 1（接入 make check）
 *   node scripts/check-conventions.mjs --update   # 重新生成基线（存量登记；阶段 2 首次落地用）
 *   node scripts/check-conventions.mjs --report   # 报告模式：列出全部存量 + 报告型检查项，恒 exit 0
 *
 * 阻塞规则（①-④，纳入 baseline）：
 *   ① font-size             字号必须命中允许清单；拦 text-[NNpx]（含 10px/15px 等任意值）与清单外命名字号（text-lg/text-3xl…）
 *   ② color-literal         tsx 内不得出现颜色字面量（#hex / rgb( / hsl( / oklch( / color-mix(）；
 *                           豁免：BrandMark.tsx（品牌图形）
 *   ③ long-classname-line   含 className 且 >200 字符的行必须抽组件或常量化（方案 §6.5）
 *   ④ pages-api-value-import  pages/** 对 @/api 的值导入 = 0（import type 与内联 { type X } 均放行）
 *
 * 报告规则（⑤⑥，只打印不判失败）：
 *   ⑤ english-copy   tsx 交互文案 + i18n 字典的英文候选（白名单见 ENGLISH_WHITELIST）
 *   ⑥ route-parity   src/nav/nav.ts 路由清单 ↔ internal/server/static_assets_test.go 的 SPA 兜底数组
 *
 * 扫描范围：src/**（排除 api / hooks / lib 白名单区——数据层与纯函数区另由 hooks 规格约束）。
 *
 * baseline 机制（方案 §12 收口路径）：
 *   基线 = 当前存量，结构「规则 → 文件 → 匹配键 → 计数」。匹配键取稳定片段
 *   （字号类名 / 颜色字面量 / @/api 模块名；③ 无稳定片段，退化为按文件的超长行数棘轮），
 *   因此行号漂移、乃至对违规行的文案/token 改写都不会假报「新增」——迁移批次可以放心动行。
 *   默认模式只对「超出基线配额的部分」报错 ⇒ 阶段 3 存量不挡路、新违规立即拦住；
 *   每批迁移完成后用 --update 收缩基线，阶段 4 清空转严格（基线清空后任何违规都 exit 1）。
 *
 * 实现注记（与方案 §8.3-8 的有意偏差）：本脚本保持 Node ESM 零依赖（不 import typescript，
 * 不引入新包）。④ 的 import 解析是手写语法级解析：`import type {...}` 与内联 `{ type X }`
 *   均正确放行，默认导入 / 命名空间导入 / 混合导入（值 + type）判为值导入。局限是「不做类型
 *   语义分析」——把类型别名当值使用的写法由 tsc 兜底，本脚本不重复承担。
 */
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const FRONTEND_DIR = fileURLToPath(new URL('..', import.meta.url))
const REPO_DIR = join(FRONTEND_DIR, '..')
const SRC_DIR = join(FRONTEND_DIR, 'src')
const BASELINE_FILE = join(FRONTEND_DIR, 'scripts', 'conventions-baseline.json')

const BLOCKING_RULES = [
  { id: 'font-size', title: '非阶梯字号（允许清单外）' },
  { id: 'color-literal', title: 'tsx 内颜色字面量' },
  { id: 'long-classname-line', title: '含 className 且超过 200 字符的行' },
  { id: 'pages-api-value-import', title: 'pages 对 @/api 的值导入' },
]

/** 扫描白名单区：数据层（api）、通用 hooks、纯函数库不在本护栏职责内。 */
const EXCLUDED_TOP_DIRS = new Set(['api', 'hooks', 'lib'])

/** ① 允许的字号类（方案 §6.2 阶梯：11/12/13/14/16/20/24/28）。 */
const ALLOWED_FONT_SIZE_CLASSES = new Set([
  'text-micro',
  'text-caption',
  'text-xs',
  'text-sm',
  'text-base',
  'text-xl',
  'text-2xl',
  'text-display',
])

/** Tailwind 命名字号全集：不在允许清单内的即违规（text-lg / text-3xl / text-2xs…）。 */
const NAMED_FONT_SIZE_RE = /\btext-(2xs|xs|sm|base|md|lg|xl|2xl|3xl|4xl|5xl|6xl|7xl|8xl|9xl|micro|caption)\b/g
/** 任意值字号：text-[10px] / text-[13px] 等（颜色型任意值交 ② 处理）。 */
const ARBITRARY_TEXT_RE = /\btext-\[([^\]]*)\]/g
const COLOR_LIKE_ARBITRARY_RE = /^(#|rgb|rgba|hsl|hsla|oklch|oklab|color-mix|color:|var\(--(color|chart|accent|ink|signal|info|success|warning|danger|link|text|border|surface|focus))/

/** ② 颜色字面量（豁免文件见方案 §6.1 清单）。 */
const COLOR_EXEMPT_FILES = new Set(['BrandMark.tsx'])
const COLOR_LITERAL_PATTERNS = [
  /#[0-9a-fA-F]{3,8}\b/g,
  /\brgba?\(/g,
  /\bhsla?\(/g,
  /\boklch\(/g,
  /\boklab\(/g,
  /\bcolor-mix\(/g,
]

/** ⑤ 英文候选白名单（术语允许保留；新增术语在此登记，别在业务代码里放宽）。 */
const ENGLISH_WHITELIST = new Set([
  // 术语（方案 §8.3-1 明示可保留）
  'Token', 'Tokens', 'API', 'APIs', 'OAuth', 'PAT', 'URL', 'URLs', 'SQLite', 'CLI', 'Provider', 'Providers',
  'JSON', 'IDE', 'curl', 'Rewarm', 'HTTP', 'HTTPS', 'SOCKS5', 'SSE', 'CSV', 'OK', 'ID', 'IP', 'CPU', 'RSS',
  'GPU', 'MB', 'GB', 'KB', 'ms', 'CLIProxyAPI', 'TLS', 'SSL', 'Proxy', 'Key', 'Keys', 'Machine', 'Base',
  'Bearer', 'Chat', 'Completions', 'Messages', 'Responses', 'p50', 'p95', 'TTFB', 'Agent',
  // 品牌 / 产品 / 模型名
  'agent2api', 'Claude', 'Code', 'Gemini', 'GPT', 'Trae', 'WorkBuddy', 'OpenAI', 'Anthropic',
  'GitHub', 'Windows', 'macOS', 'Linux', 'Docker', 'Node', 'npm', 'GSAP', 'HeroUI', 'Tailwind', 'Vite',
  'React', 'glm', 'qwen', 'deepseek',
  // 上游活动专名（方案 §4.4 沿用）
  'Travel',
  // HTTP 方法
  'DELETE', 'GET', 'POST', 'PUT', 'PATCH',
])

/** ⑤ 带字面量值的交互属性：其值为纯英文即候选（低噪声、高价值）。 */
const COPY_ATTRIBUTE_RE = /\b(?:aria-label|placeholder|title)="([^"]*)"/g

// ── 文件遍历 ─────────────────────────────────────────────────────────

function walk(dir, out = []) {
  for (const name of readdirSync(dir).sort()) {
    const full = join(dir, name)
    if (statSync(full).isDirectory()) walk(full, out)
    else out.push(full)
  }
  return out
}

function toPosix(path) {
  return path.split(sep).join('/')
}

/**
 * 收集 src 下的待扫描文件：排除 src/api、src/hooks、src/lib（白名单区）。
 * 返回 { rel, abs, text, lines } 列表。
 */
function scanTargets() {
  const files = []
  for (const abs of walk(SRC_DIR)) {
    const rel = toPosix(relative(FRONTEND_DIR, abs))
    const inner = rel.slice('src/'.length)
    const top = inner.split('/')[0]
    if (EXCLUDED_TOP_DIRS.has(top)) continue
    if (!/\.(tsx|ts)$/.test(rel)) continue
    files.push({ rel, abs })
  }
  return files
}

// ── 阻塞规则实现 ─────────────────────────────────────────────────────
// 每条违规都带 `key`：baseline 的匹配指纹（见「基线读写」节）。
// 有稳定片段可用（字号类名 / 颜色字面量 / @/api 模块名）就用它当 key；
// ③ 的违规没有稳定片段（长度本身就是判据），退化为按文件计数棘轮。

function collectFontSizeViolations(file, lines) {
  if (!file.rel.endsWith('.tsx')) return []
  const found = []
  lines.forEach((line, index) => {
    for (const match of line.matchAll(ARBITRARY_TEXT_RE)) {
      const inner = match[1].trim()
      if (COLOR_LIKE_ARBITRARY_RE.test(inner)) continue // 颜色型任意值由 ② 口径处理
      found.push({ rule: 'font-size', file: file.rel, line: index + 1, snippet: line.trim(), match: match[0], key: match[0] })
    }
    for (const match of line.matchAll(NAMED_FONT_SIZE_RE)) {
      if (ALLOWED_FONT_SIZE_CLASSES.has(match[0])) continue
      found.push({ rule: 'font-size', file: file.rel, line: index + 1, snippet: line.trim(), match: match[0], key: match[0] })
    }
  })
  return found
}

function collectColorLiteralViolations(file, lines) {
  if (!file.rel.endsWith('.tsx')) return []
  const base = file.rel.split('/').pop()
  if (COLOR_EXEMPT_FILES.has(base)) return []
  const found = []
  lines.forEach((line, index) => {
    for (const pattern of COLOR_LITERAL_PATTERNS) {
      for (const match of line.matchAll(pattern)) {
        found.push({ rule: 'color-literal', file: file.rel, line: index + 1, snippet: line.trim(), match: match[0], key: match[0] })
      }
    }
  })
  return found
}

const LONG_CLASSNAME_LIMIT = 200

function collectLongClassnameViolations(file, lines) {
  if (!file.rel.endsWith('.tsx')) return []
  const found = []
  lines.forEach((line, index) => {
    if (line.includes('className') && line.length > LONG_CLASSNAME_LIMIT) {
      found.push({ rule: 'long-classname-line', file: file.rel, line: index + 1, snippet: line.trim(), match: `${line.length} 字符`, key: '__count__' })
    }
  })
  return found
}

/**
 * ④ 值导入判定：`import type {...}` 与内联 `{ type X, type Y }` 放行；
 * 默认导入 / 命名空间导入 / 含运行期值的命名导入判为值导入。
 */
function importIsValueOnly(clause) {
  const trimmed = clause.trim()
  if (/^type\b/.test(trimmed)) return false
  const brace = trimmed.match(/\{([\s\S]*)\}/)
  if (!brace) return true // `import api from …` / `import * as api from …`
  const beforeBrace = trimmed.slice(0, brace.index).replace(/,\s*$/, '').trim()
  if (beforeBrace) return true // 默认导入 + 命名导入混写：默认导入是值
  const specifiers = brace[1].split(',').map((item) => item.trim()).filter(Boolean)
  if (specifiers.length === 0) return true
  return !specifiers.every((item) => /^type\s/.test(item))
}

function isApiModule(specifier) {
  return specifier === '@/api' || specifier.startsWith('@/api/')
}

function collectPagesApiValueImports(file, text) {
  if (!file.rel.startsWith('src/pages/')) return []
  const found = []
  const lineOf = (index) => text.slice(0, index).split('\n').length
  const staticImportRe = /^import\s+([\s\S]*?)\s*from\s*(['"])([^'"]+)\2/gm
  for (const match of text.matchAll(staticImportRe)) {
    const [, clause, , specifier] = match
    if (!isApiModule(specifier)) continue
    if (!importIsValueOnly(clause)) continue
    const line = lineOf(match.index)
    found.push({
      rule: 'pages-api-value-import',
      file: file.rel,
      line,
      snippet: `import ${clause.trim().replace(/\s+/g, ' ')} from '${specifier}'`,
      match: specifier,
      key: specifier,
    })
  }
  const dynamicImportRe = /\bimport\(\s*(['"])([^'"]+)\1\s*\)/g
  for (const match of text.matchAll(dynamicImportRe)) {
    const specifier = match[2]
    if (!isApiModule(specifier)) continue
    found.push({
      rule: 'pages-api-value-import',
      file: file.rel,
      line: lineOf(match.index),
      snippet: `import('${specifier}')`,
      match: specifier,
      key: specifier,
    })
  }
  return found
}

function collectBlockingViolations() {
  const violations = []
  for (const file of scanTargets()) {
    const text = readFileSync(file.abs, 'utf8')
    const lines = text.split('\n')
    violations.push(
      ...collectFontSizeViolations(file, lines),
      ...collectColorLiteralViolations(file, lines),
      ...collectLongClassnameViolations(file, lines),
      ...collectPagesApiValueImports(file, text),
    )
  }
  return violations
}

// ── 报告规则实现 ─────────────────────────────────────────────────────

/** JSX 文本节点；`(?<!=)` 排除箭头函数返回类型（`=> Promise<void>` 会被误当日志文本）。 */
const JSX_TEXT_RE = /(?<!=)>([^<>{}]+)</g
/** 排除表达式残片 / 占位符模板造成的假文本命中。 */
const CODE_LIKE_RE = /[=&;?()[\]|]|\/\//
const PLACEHOLDER_RE = /\{[^}]*\}/g

function englishCandidatesInText(rawText) {
  const text = rawText.replace(PLACEHOLDER_RE, ' ').trim()
  if (text === '' || /[\u4e00-\u9fff]/.test(text)) return [] // 含中文的混合文案不判（术语嵌中文属正常）
  if (CODE_LIKE_RE.test(text)) return []
  const words = text.match(/[A-Za-z][A-Za-z0-9'’.-]*/g) ?? []
  return words.filter((word) => word.length >= 2 && !ENGLISH_WHITELIST.has(word))
}

function collectEnglishCopyReport() {
  const hits = []
  for (const file of scanTargets()) {
    const text = readFileSync(file.abs, 'utf8')
    const lineOf = (index) => text.slice(0, index).split('\n').length
    if (file.rel.endsWith('.tsx')) {
      for (const match of text.matchAll(JSX_TEXT_RE)) {
        const words = englishCandidatesInText(match[1].trim())
        if (words.length === 0) continue
        hits.push({ file: file.rel, line: lineOf(match.index), words: [...new Set(words)].join(' '), snippet: match[1].trim() })
      }
      for (const match of text.matchAll(COPY_ATTRIBUTE_RE)) {
        const words = englishCandidatesInText(match[1])
        if (words.length === 0) continue
        hits.push({ file: file.rel, line: lineOf(match.index), words: [...new Set(words)].join(' '), snippet: `${match[0].slice(0, 80)}` })
      }
    }
    if (file.rel === 'src/i18n/messages.ts') {
      // 字典值扫描：跳过注释行与 i18n key 引用（如 idle: 'updateState_idle'）
      const dictText = text.replace(/^\s*(\/\/|\/\*).*$/gm, '')
      for (const match of dictText.matchAll(/:\s*'([^']*)'/g)) {
        const value = match[1].trim()
        if (/^[a-z][A-Za-z0-9_]*$/.test(value)) continue
        const words = englishCandidatesInText(value)
        if (words.length === 0) continue
        hits.push({ file: file.rel, line: lineOf(match.index), words: [...new Set(words)].join(' '), snippet: value })
      }
    }
  }
  return hits
}

function collectRouteParityReport() {
  const navFile = join(SRC_DIR, 'nav', 'nav.ts')
  const goFile = join(REPO_DIR, 'internal', 'server', 'static_assets_test.go')
  const lines = { nav: [], go: [], notes: [] }
  if (!existsSync(navFile)) {
    lines.notes.push(`未找到 ${toPosix(relative(REPO_DIR, navFile))}`)
    return lines
  }
  const navPaths = [...readFileSync(navFile, 'utf8').matchAll(/path:\s*'([^']+)'/g)].map((match) => match[1])
  lines.nav = [...new Set(navPaths)].sort()
  if (!existsSync(goFile)) {
    lines.notes.push(`未找到 ${toPosix(relative(REPO_DIR, goFile))}`)
    return lines
  }
  const goSource = readFileSync(goFile, 'utf8')
  const block = goSource.match(/frontendRoutes\s*:?=\s*\[\]string\{([\s\S]*?)\n\t\}/)
  if (!block) {
    lines.notes.push('未能在 static_assets_test.go 中定位 frontendRoutes 数组（写法变更请更新本脚本）')
    return lines
  }
  lines.go = [...new Set([...block[1].matchAll(/"([^"]+)"/g)].map((match) => match[1]))].sort()
  const goSet = new Set(lines.go)
  const navSet = new Set(lines.nav)
  lines.onlyNav = lines.nav.filter((path) => !goSet.has(path))
  lines.onlyGo = lines.go.filter((path) => !navSet.has(path))
  return lines
}

// ── 基线读写与比较 ───────────────────────────────────────────────────
// 匹配键 = 规则 + 文件 + 违规片段（violation.key）→ 计数。
// key 取「稳定片段」而非整行文本：字号类名 / 颜色字面量 / @/api 模块名。
// ③ long-classname-line 没有稳定片段（长度即判据），key 固定为 `__count__`
// ⇒ 按「该文件允许存在几条超长行」做棘轮。这样写的目的：迁移批次里对
// 违规行的任何改写（改文案、换 token）都不会把存量误报成新增；代价是
// 同一文件内「修掉一条又加一条同片段违规」不报警，属刻意的棘轮语义。

const COUNT_KEY = '__count__'

function fingerprintEntries(violations) {
  /** @type {Record<string, Record<string, Record<string, number>>>} */
  const byRule = {}
  for (const violation of violations) {
    const rule = (byRule[violation.rule] ??= {})
    const file = (rule[violation.file] ??= {})
    file[violation.key] = (file[violation.key] ?? 0) + 1
  }
  return byRule
}

function readBaseline() {
  if (!existsSync(BASELINE_FILE)) return null
  return JSON.parse(readFileSync(BASELINE_FILE, 'utf8'))
}

function writeBaseline(violations) {
  const byRule = fingerprintEntries(violations)
  const payload = {
    version: 1,
    note:
      '此文件由 node scripts/check-conventions.mjs --update 生成。结构：规则 → 文件 → 匹配键 → 计数；' +
      `匹配键 = 违规片段（字号类名 / 颜色字面量 / @/api 模块名），${COUNT_KEY} 表示该文件超长 className 行数的棘轮。` +
      '阶段 3 每批迁移后收紧，阶段 4 清空即转严格模式。',
    generatedAt: new Date().toISOString(),
    rules: byRule,
  }
  writeFileSync(BASELINE_FILE, `${JSON.stringify(payload, null, 2)}\n`)
  return payload
}

/**
 * 逐条抵消：当前违规命中基线配额（规则 → 文件 → 匹配键 → 计数）即视为存量；
 * 冲销不到的才是「新增」，因此行号漂移与行内容改写都不假报。
 */
function diffAgainstBaseline(violations, baseline) {
  const quota = new Map()
  for (const [rule, files] of Object.entries(baseline?.rules ?? {})) {
    for (const [file, keys] of Object.entries(files)) {
      for (const [key, count] of Object.entries(keys)) quota.set(`${rule}\u0000${file}\u0000${key}`, count)
    }
  }
  const added = []
  for (const violation of violations) {
    const key = `${violation.rule}\u0000${violation.file}\u0000${violation.key}`
    const left = quota.get(key) ?? 0
    if (left > 0) {
      quota.set(key, left - 1)
      continue
    }
    added.push(violation)
  }
  return { added }
}

function countBaseline(baseline) {
  let total = 0
  for (const files of Object.values(baseline?.rules ?? {})) {
    for (const keys of Object.values(files)) {
      for (const count of Object.values(keys)) total += count
    }
  }
  return total
}

// ── 输出 ─────────────────────────────────────────────────────────────

function formatViolation(violation) {
  return `[${violation.rule}] ${violation.file}:${violation.line}  ${violation.match}  ← ${violation.snippet.slice(0, 120)}`
}

function printReport(violations) {
  console.log('（--report 报告模式：以下为全部存量，不判失败）')
  for (const rule of BLOCKING_RULES) {
    const list = violations.filter((violation) => violation.rule === rule.id)
    console.log(`\n── ①-④ ${rule.id}：${rule.title} —— 共 ${list.length} 处`)
    if (list.length === 0) {
      console.log('   （无）')
      continue
    }
    const shown = list.slice(0, 30)
    for (const violation of shown) console.log(`   · ${formatViolation(violation)}`)
    if (list.length > shown.length) console.log(`   …（其余 ${list.length - shown.length} 处省略）`)
  }

  console.log('\n── ⑤ english-copy：tsx 交互文案 + i18n 字典英文候选（仅报告）')
  const english = collectEnglishCopyReport()
  if (english.length === 0) console.log('   （无）')
  for (const hit of english.slice(0, 30)) console.log(`   · ${hit.file}:${hit.line}  [${hit.words}]  ← ${hit.snippet.slice(0, 80)}`)
  if (english.length > 30) console.log(`   …（其余 ${english.length - 30} 处省略；白名单见脚本 ENGLISH_WHITELIST）`)

  console.log('\n── ⑥ route-parity：nav.ts 页面路由 ↔ 后端 SPA 兜底遍历数组（仅报告）')
  const parity = collectRouteParityReport()
  for (const note of parity.notes) console.log(`   ! ${note}`)
  console.log(`   nav.ts 页面路由（${parity.nav.length}）：${parity.nav.join(' ')}`)
  console.log(`   后端兜底数组（${parity.go.length}）：${parity.go.join(' ')}`)
  if (parity.onlyNav) console.log(`   nav.ts 有、后端清单无（需补后端测试列表）：${parity.onlyNav.join(' ') || '（无）'}`)
  if (parity.onlyGo) console.log(`   后端清单有、nav.ts 无（预期含 /login 与兼容重定向，逐条核对）：${parity.onlyGo.join(' ') || '（无）'}`)
}

function main() {
  const args = new Set(process.argv.slice(2))
  const violations = collectBlockingViolations()

  if (args.has('--help') || args.has('-h')) {
    console.log('用法：node scripts/check-conventions.mjs [--update|--report]')
    return 0
  }

  if (args.has('--report')) {
    console.log(`前端约定护栏（报告模式）：扫描 src/**（排除 ${[...EXCLUDED_TOP_DIRS].join(' / ')}）`)
    printReport(violations)
    return 0
  }

  if (args.has('--update')) {
    const payload = writeBaseline(violations)
    const total = countBaseline(payload)
    console.log(`已重写基线：${toPosix(relative(REPO_DIR, BASELINE_FILE))}（${total} 处存量）`)
    for (const rule of BLOCKING_RULES) {
      const list = violations.filter((violation) => violation.rule === rule.id)
      console.log(`  · ${rule.id.padEnd(24)} ${String(list.length).padStart(4)} 处`)
    }
    return 0
  }

  const baseline = readBaseline()
  if (!baseline) {
    console.error(`缺少基线文件 ${toPosix(relative(REPO_DIR, BASELINE_FILE))}；首次落地请先跑 node scripts/check-conventions.mjs --update`)
    return 1
  }
  const { added } = diffAgainstBaseline(violations, baseline)
  const baselineTotal = countBaseline(baseline)
  console.log(`前端约定护栏：扫描 src/**（排除 ${[...EXCLUDED_TOP_DIRS].join(' / ')}）；基线存量 ${baselineTotal} 处、当前 ${violations.length} 处`)
  for (const rule of BLOCKING_RULES) {
    const current = violations.filter((violation) => violation.rule === rule.id).length
    const known = countBaseline({ rules: { [rule.id]: baseline.rules?.[rule.id] ?? {} } })
    console.log(`  · ${rule.id.padEnd(24)} 当前 ${String(current).padStart(4)} / 基线 ${String(known).padStart(4)}${current > known ? '  ↑' : ''}`)
  }
  if (added.length === 0) {
    console.log('结果：无新增违规 ✓（存量收缩后请用 --update 收紧基线）')
    return 0
  }
  console.error(`结果：新增 ${added.length} 处违规（不在基线内，需修复或确认后 --update）`)
  for (const violation of added.slice(0, 20)) console.error(`  ✗ ${formatViolation(violation)}`)
  if (added.length > 20) console.error(`  …（其余 ${added.length - 20} 处省略）`)
  return 1
}

process.exit(main())
