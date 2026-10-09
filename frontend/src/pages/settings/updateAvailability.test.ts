import { describe, expect, it } from 'vitest'

import type { SystemUpdateInfo } from '@/api/system'
import { updateAvailability } from './updateAvailability'

function info(overrides: { managed?: boolean; available?: boolean } = {}): SystemUpdateInfo {
  return {
    current_version: '1.2.3',
    has_update: true,
    managed: overrides.managed ?? true,
    cached: false,
    agent: { available: overrides.available ?? true, state: 'idle' },
  }
}

describe('updateAvailability', () => {
  it('未加载（info=null）时不给结论', () => {
    expect(updateAvailability(null)).toEqual({ ready: false, reasons: [] })
  })

  it('已发布 Release + 更新器可达：可以更新', () => {
    expect(updateAvailability(info())).toEqual({ ready: true, reasons: [] })
  })

  it('开发镜像 / 本地构建：提示必须是已发布 Release', () => {
    expect(updateAvailability(info({ managed: false }))).toEqual({ ready: false, reasons: ['updaterNeedRelease'] })
  })

  it('更新器不可达：给出「需宿主机更新器」两条修复指引', () => {
    expect(updateAvailability(info({ available: false }))).toEqual({
      ready: false,
      reasons: ['updaterNeedsHost', 'updaterNeedInstall', 'updaterNeedCompose'],
    })
  })

  it('两个原因同时存在：按 发布形态 → 更新器 顺序列出', () => {
    expect(updateAvailability(info({ managed: false, available: false }))).toEqual({
      ready: false,
      reasons: ['updaterNeedRelease', 'updaterNeedsHost', 'updaterNeedInstall', 'updaterNeedCompose'],
    })
  })
})
