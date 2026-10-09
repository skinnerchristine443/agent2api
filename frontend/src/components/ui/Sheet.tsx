import type { ReactNode } from 'react'
import { Drawer, useOverlayState } from '@heroui/react'

/** 抽屉方位。详情阅读默认右侧；移动端优先的流程可传 `bottom`。 */
export type SheetPlacement = 'top' | 'bottom' | 'left' | 'right'

type Props = {
  /** 受控显隐（与 `onOpenChange` 成对使用）。 */
  isOpen: boolean
  /** 显隐变更回调：关闭动作（Esc / 遮罩 / 关闭按钮）统一从这里回来。 */
  onOpenChange: (isOpen: boolean) => void
  /** 标题（渲染在抽屉头，同时作为无障碍标题）。 */
  title: ReactNode
  children: ReactNode
  /** 抽屉方位，默认 `right`。 */
  placement?: SheetPlacement
  /** 关闭按钮的无障碍标签，默认「关闭」。 */
  closeLabel?: string
  /** 抽屉面板附加类名（宽度 / 内边距等）。 */
  className?: string
}

/**
 * 详情抽屉原语：受控显隐，基于 HeroUI `Drawer.Root` + `useOverlayState`
 * （`Drawer.Root` 接受 `state` 受控回归，见 `@heroui/react` 类型定义）。
 *
 * 定位（方案 §6.5）：详情承载的移动端友好形态，日志详情首用。URL 协议
 * 由 `useSheetUrlParam` 配套提供——打开 push 写参、关闭 replace 删参，
 * 使详情可直达、可分享，且「后退」不会重开已关闭的详情。
 *
 * 组件本身不写 URL、不取数：显隐状态与数据由调用方（页面 + URL hook）持有。
 */
export function Sheet({ isOpen, onOpenChange, title, children, placement = 'right', closeLabel = '关闭', className }: Props) {
  const state = useOverlayState({ isOpen, onOpenChange })
  return (
    <Drawer.Root state={state}>
      <Drawer.Content placement={placement} className={className}>
        <Drawer.Dialog>
          <Drawer.Header className="flex items-center justify-between gap-3">
            <Drawer.Heading className="min-w-0 text-sm font-semibold text-foreground">{title}</Drawer.Heading>
            <Drawer.CloseTrigger aria-label={closeLabel} />
          </Drawer.Header>
          <Drawer.Body>{children}</Drawer.Body>
        </Drawer.Dialog>
      </Drawer.Content>
    </Drawer.Root>
  )
}
