/** 状态圆点语义（固定映射：ok=success、warn=warning、danger=danger、muted=中性灰）。 */
export type StatusDotState = 'ok' | 'warn' | 'danger' | 'muted'

type Props = {
  state: StatusDotState
  className?: string
}

/**
 * 状态圆点原语：包裹既有 `.status-dot` BEM class（保留其 CSS 变量
 * 配色逻辑与 `.account-card` 下的尺寸收敛），把散落的
 * `<span className="status-dot" data-state=… />` 收敛为组件（方案 §6.5）。
 *
 * 仅表达状态色，不携带文本——语义由相邻文案承载，因此标记
 * `aria-hidden`，避免读屏重复播报。
 */
export function StatusDot({ state, className }: Props) {
  return <span aria-hidden="true" className={['status-dot', className].filter(Boolean).join(' ')} data-state={state} />
}