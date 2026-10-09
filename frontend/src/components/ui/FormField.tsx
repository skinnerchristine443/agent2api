import type { ReactNode } from 'react'
import { Label } from '@heroui/react'

type Props = {
  /** 字段名（渲染在控件上方）。 */
  label: string
  /** 表单控件插槽（Input / Textarea / Select 等）。 */
  children: ReactNode
  /** 辅助说明（渲染在控件下方；与 error 互斥，error 优先）。 */
  description?: ReactNode
  /** 校验错误文案（存在时顶替 description，并以 danger 色 + role=alert 呈现）。 */
  error?: ReactNode
  /** 与控件的 id 关联（无障碍必需；无原生控件时省略）。 */
  htmlFor?: string
  className?: string
}

/**
 * 纵向表单字段原语：Label 在上 → 控件 → 说明 / 错误。
 *
 * 边界（方案 §6.5）：与 `FormRow`（**横向设置行**，标签在左）职责不同——
 * FormRow 用于设置面板（标签列对齐成栅格），FormField 用于表单（新建 /
 * 编辑流程，字段纵向堆叠、支持逐字段报错）。**同页不混用**：同一页面
 * 只用其中一种，避免两种标签基线并存（边界已登记 docs/04）。
 */
export function FormField({ label, children, description, error, htmlFor, className }: Props) {
  return (
    <div className={['flex flex-col gap-1.5', className].filter(Boolean).join(' ')}>
      <Label htmlFor={htmlFor} className="text-sm font-medium text-foreground">{label}</Label>
      <div className="min-w-0 [&_[data-slot=input]]:w-full [&_[data-slot=textarea]]:w-full">{children}</div>
      {error ? (
        <p role="alert" className="text-xs leading-5 text-danger">{error}</p>
      ) : description ? (
        <p className="text-xs leading-5 text-muted">{description}</p>
      ) : null}
    </div>
  )
}
