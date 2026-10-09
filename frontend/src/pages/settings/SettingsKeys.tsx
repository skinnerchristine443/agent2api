import { useEffect, useState } from 'react'
import type { KeyTarget } from '@/api/keys'
import { PageAlert } from '@/components/ui/PageAlert'
import { useApiKeys } from '@/hooks/useApiKeys'

import { ConsoleKeySection } from './ConsoleKeySection'
import { KeySemanticsCard } from './KeySemanticsCard'
import { ProxyKeySection } from './ProxyKeySection'
import { RotateKeyDialog } from './RotateKeyDialog'
import { SecretRevealModal } from './SecretRevealModal'

/**
 * 设置 · 密钥页签（批次 4a 收编，原密钥管理页）：管理面 / 数据面两把固定
 * 角色钥的读取（仅 console 有指纹）与轮换。
 *
 * 轮换安全四件套（方案 §4.4 ⑫ P0）：
 * ① 原子写回 + ② 竞态抑制在 `useApiKeys`（含 setApiKey 顺序与 signOut 暂缓）；
 * ③ 显式保存确认在 `SecretRevealModal`（未确认不得关闭）；
 * ④ 二次确认在 `RotateKeyDialog`（按目标提示失效范围）。
 */
export function SettingsKeys() {
  const [error, setError] = useState('')
  const [pendingTarget, setPendingTarget] = useState<KeyTarget | null>(null)
  const keys = useApiKeys(setError)
  const { load, rotate } = keys
  const busy = keys.busyTarget != null

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [load])

  // 轮换成功：明文已在手（先写回、后展示），收起确认框；失败则留在框内看报错。
  async function confirmRotate() {
    const target = pendingTarget
    if (!target) return
    if (await rotate(target)) setPendingTarget(null)
  }

  return (
    <div className="space-y-5">
      {error ? <PageAlert title={error} /> : null}

      <div className="grid items-start gap-5 xl:grid-cols-2">
        <ConsoleKeySection
          consoleKey={keys.consoleKey}
          busy={keys.busyTarget === 'console'}
          revealed={keys.revealed.console}
          onReveal={() => void keys.reveal('console')}
          onHide={() => keys.hideReveal('console')}
          onRotate={() => setPendingTarget('console')}
        />
        <ProxyKeySection
          busy={keys.busyTarget === 'proxy'}
          revealed={keys.revealed.proxy}
          onReveal={() => void keys.reveal('proxy')}
          onHide={() => keys.hideReveal('proxy')}
          onRotate={() => setPendingTarget('proxy')}
        />
      </div>

      <KeySemanticsCard />

      <RotateKeyDialog
        target={pendingTarget}
        busy={busy}
        onClose={() => { if (!busy) setPendingTarget(null) }}
        onConfirm={confirmRotate}
      />

      <SecretRevealModal rotated={keys.rotated} onClose={() => keys.setRotated(null)} />
    </div>
  )
}
