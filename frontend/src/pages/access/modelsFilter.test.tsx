// @vitest-environment happy-dom
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, useSearchParams } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { usePagedQuery } from '@/hooks/usePagedQuery'
import { translate } from '@/i18n/messages'
import type { ModelInfo } from '@/api/types'

import { filterModels, modelsFiltersKey, providerRegionOptions, splitProviderFilter } from './modelsFilter'
import { FILTER_DEBOUNCE_MS, useModelsFilters } from './useModelsFilters'

function model(overrides: Partial<ModelInfo> & { id: string }): ModelInfo {
  return { ...overrides }
}

describe('modelsFilter（纯函数）', () => {
  it('provider 筛选解析 `渠道:区域`，无冒号视为渠道', () => {
    expect(splitProviderFilter('trae:cn')).toEqual({ provider: 'trae', region: 'cn' })
    expect(splitProviderFilter('workbuddy')).toEqual({ provider: 'workbuddy', region: '' })
    expect(splitProviderFilter('')).toEqual({ provider: '', region: '' })
  })

  it('渠道筛选按 provider + region 过滤；区域缺省不参与', () => {
    const models = [
      model({ id: 'glm', provider: 'workbuddy', region: 'global' }),
      model({ id: 'glm', provider: 'workbuddy', region: 'cn' }),
      model({ id: 'solo', provider: 'trae', region: 'cn' }),
    ]
    const onlyCNWorkBuddy = filterModels(models, { q: '', provider: 'workbuddy:cn' }, translate)
    expect(onlyCNWorkBuddy).toHaveLength(1)
    expect(onlyCNWorkBuddy[0].region).toBe('cn')
    expect(filterModels(models, { q: '', provider: 'trae' }, translate)).toHaveLength(1)
  })

  it('搜索命中模型名 / id / 上游 key / 倍率；大小写与首尾空格不敏感', () => {
    const models = [
      model({ id: 'glm-5.3', display_name: 'GLM 5.3', credits: 'x1.5' }),
      model({ id: 'deepseek-v4', mapped_key: 'ds-v4', provider: 'trae', region: 'cn' }),
    ]
    expect(filterModels(models, { q: ' glm ', provider: '' }, translate)).toHaveLength(1)
    expect(filterModels(models, { q: 'DS-V4', provider: '' }, translate)).toHaveLength(1)
    expect(filterModels(models, { q: 'x1.5', provider: '' }, translate)).toHaveLength(1)
    expect(filterModels(models, { q: 'no-such-model', provider: '' }, translate)).toHaveLength(0)
  })

  it('providerRegionOptions 只收有区域 id 的描述符', () => {
    const options = providerRegionOptions([
      {
        id: 'workbuddy',
        label: 'WorkBuddy',
        runtime: 'in_process',
        capabilities: { browser_login: true, pat_login: true, import_export: true },
        regions: [{ id: 'cn', label: '国内版' }, { id: 'global', label: '国际版' }],
        default_region: 'global',
      },
      {
        id: 'trae',
        label: 'Trae',
        runtime: 'in_process',
        capabilities: { browser_login: true, pat_login: false, import_export: true },
        regions: [],
        default_region: 'cn',
      },
    ])
    expect(options.map((option) => option.value)).toEqual(['workbuddy:cn', 'workbuddy:global'])
  })

  it('筛选稳定键随 q / provider 变化（驱动分页回落第 1 页）', () => {
    expect(modelsFiltersKey({ q: 'glm', provider: '' })).not.toBe(modelsFiltersKey({ q: 'glm', provider: 'trae:cn' }))
    expect(modelsFiltersKey({ q: ' glm ', provider: '' })).toBe(modelsFiltersKey({ q: 'glm', provider: '' }))
  })
})

// ── URL 状态接线（与 ModelsPage 相同：filters + usePagedQuery） ─────────────

function Harness() {
  const filters = useModelsFilters()
  const paged = usePagedQuery({ defaultSize: 50, filtersKey: filters.filtersKey })
  const [params] = useSearchParams()
  return (
    <div>
      <input aria-label="搜索" value={filters.q} onChange={(event) => filters.setQ(event.target.value)} />
      <button type="button" onClick={() => filters.setProvider('workbuddy:cn')}>切成国内渠道</button>
      <button type="button" onClick={() => filters.setProvider('')}>全部渠道</button>
      <span data-testid="page">{paged.page}</span>
      <span data-testid="size">{paged.size}</span>
      <span data-testid="search">{params.toString()}</span>
    </div>
  )
}

function renderHarness(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Harness />
    </MemoryRouter>,
  )
}

describe('模型目录筛选 × 分页（URL 化）', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('直链读取筛选与分页：?q=&provider=&page=&size=', () => {
    renderHarness('/models?page=3&size=20&q=glm&provider=trae:cn')
    expect(screen.getByTestId('page').textContent).toBe('3')
    expect(screen.getByTestId('size').textContent).toBe('20')
    expect((screen.getByLabelText('搜索') as HTMLInputElement).value).toBe('glm')
  })

  it('筛选变化重置分页：URL 带 page=3 + 新渠道筛选 → 落第 1 页且 page 参数被删除', () => {
    renderHarness('/models?page=3&size=20&q=glm')
    expect(screen.getByTestId('page').textContent).toBe('3')

    fireEvent.click(screen.getByRole('button', { name: '切成国内渠道' }))
    expect(screen.getByTestId('page').textContent).toBe('1')
    const search = screen.getByTestId('search').textContent || ''
    expect(search).toContain('provider=workbuddy%3Acn')
    expect(search).toContain('size=20') // 其余参数保留
    expect(search).not.toContain('page=') // 默认页码不写入 URL
  })

  it('清除渠道筛选：默认值不写入 URL', () => {
    renderHarness('/models?provider=trae:cn')
    fireEvent.click(screen.getByRole('button', { name: '全部渠道' }))
    expect(screen.getByTestId('search').textContent || '').not.toContain('provider=')
  })

  it('文本搜索防抖 280ms 后才写 URL（写前不污染链接）', () => {
    renderHarness('/models?page=2')
    fireEvent.change(screen.getByLabelText('搜索'), { target: { value: 'deepseek' } })

    expect(screen.getByTestId('search').textContent || '').not.toContain('q=')
    act(() => { vi.advanceTimersByTime(FILTER_DEBOUNCE_MS - 1) })
    expect(screen.getByTestId('search').textContent || '').not.toContain('q=')

    act(() => { vi.advanceTimersByTime(1) })
    const search = screen.getByTestId('search').textContent || ''
    expect(search).toContain('q=deepseek')
    // 文本筛选同样重置分页
    expect(screen.getByTestId('page').textContent).toBe('1')
  })
})
