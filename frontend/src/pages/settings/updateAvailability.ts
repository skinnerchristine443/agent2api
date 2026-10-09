import type { SystemUpdateInfo } from '@/api/system'
import type { DictKey } from '@/i18n/messages'

export type UpdateAvailability = {
  /** 这台机器能否安装更新（提前诊断，避免下载完才发现装不了）。 */
  ready: boolean
  /** 不可用的原因与修复指引（本地 i18n key；不渲染后端英文 hint）。 */
  reasons: DictKey[]
}

/**
 * 更新器可用性诊断（方案 §4.4 ⑪ ①）：
 * ① 当前运行的不是已发布 Release（`managed=false`，如本地 / 开发镜像）；
 * ② 宿主机更新器不可达（没有安装 → `deploy/install-updater.sh`；
 *    或容器没挂上更新器 socket——Linux 需挂载，Docker Desktop 用回环地址）。
 *
 * 后端未区分 ② 的两种成因（`agent.available=false` 只有一个布尔），故两条
 * 指引一并给出，由部署者按环境自取。
 */
export function updateAvailability(info: SystemUpdateInfo | null): UpdateAvailability {
  if (!info) return { ready: false, reasons: [] }
  const reasons: DictKey[] = []
  if (!info.managed) reasons.push('updaterNeedRelease')
  if (!info.agent?.available) reasons.push('updaterNeedsHost', 'updaterNeedInstall', 'updaterNeedCompose')
  return { ready: reasons.length === 0, reasons }
}
