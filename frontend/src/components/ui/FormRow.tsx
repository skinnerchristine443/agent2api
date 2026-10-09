import type { ReactNode } from 'react'
import { Label } from '@heroui/react'

type Props = {
  label: string
  /** 可选的辅助说明文字，渲染在控件下方，与控件列对齐。 */
  hint?: ReactNode
  /** 标签列宽度。默认 7rem，足以容纳 CJK 标签加尾部冒号。 */
  labelWidthClass?: string
  htmlFor?: string
  children: ReactNode
}

const defaultLabelWidthClass = 'w-28'

/**
 * 每行一个标签 + 一个控件：标签固定在左侧，控件填满
 * 其余空间。让 console 中的每个表单（账号创建/编辑、系统
 * 设置、key 创建）都对齐到同一套栅格。
 */
export function FormRow({ label, hint, labelWidthClass = defaultLabelWidthClass, htmlFor, children }: Props) {
  return (
    <div>
      <div className="flex items-center gap-3">
        <Label htmlFor={htmlFor} className={`${labelWidthClass} shrink-0 text-sm font-medium text-foreground`}>{label}</Label>
        <div className="min-w-0 flex-1 [&_[data-slot=input]]:w-full [&_[data-slot=textarea]]:w-full">{children}</div>
      </div>
      {hint ? <p className="mt-1.5 pl-[7.75rem] text-xs leading-5 text-muted">{hint}</p> : null}
    </div>
  )
}
