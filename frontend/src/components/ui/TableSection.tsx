import type { ReactNode, TableHTMLAttributes } from 'react'

type Props = {
  /** 表头单元格内容（按列顺序）。空字符串 = 空表头。 */
  head: ReactNode[]
  children: ReactNode
  /** 是否包一层 `overflow-x-auto` 滚动容器（宽表用；默认 true）。 */
  scroll?: boolean
  /** 表格最小宽度类（如 `min-w-[720px]`）。 */
  minWidthClass?: string
  className?: string
}

/**
 * 静态汇总表原语（设计规范 v3）。
 *
 * 收敛「原生 `<table>` + 手写 thead」三处重复的字面量
 * `text-micro font-medium tracking-[0.08em] text-muted uppercase`，
 * 统一表头样式与 cell padding（`px-3 py-2`）。
 *
 * 适用：只读汇总表（用量分组 / 模型×账号 / 到期额度 / 渠道参数矩阵）。
 * **不适用**：交互数据表（排序/行点击）用 HeroUI `<Table>`；
 *          账号池 12 列行动态表用其专用 `.acct-*` 栅格。
 */
export function TableSection({ head, children, scroll = true, minWidthClass, className }: Props) {
  return (
    <div className={scroll ? 'overflow-x-auto' : undefined}>
      <table className={['w-full border-collapse text-sm', minWidthClass, className].filter(Boolean).join(' ')}>
        <thead>
          <tr className="border-b border-separator bg-surface-sunken text-left">
            {head.map((cell, index) => (
              // 表头为纯展示、列序稳定，用索引作 key 可接受。
              <th
                key={index}
                className="whitespace-nowrap px-3 py-2 text-micro font-medium uppercase tracking-[0.08em] text-muted"
              >
                {cell}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  )
}

/** 汇总表行（`<tr>`），统一底部分隔线。 */
export function TableSectionRow({ children, className, ...rest }: TableHTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr className={['border-b border-separator last:border-b-0', className].filter(Boolean).join(' ')} {...rest}>
      {children}
    </tr>
  )
}

/** 汇总表单元格（`<td>`），统一 `px-3 py-2`。 */
export function TableSectionCell({ children, className, ...rest }: TableHTMLAttributes<HTMLTableCellElement>) {
  return (
    <td className={['px-3 py-2 align-middle', className].filter(Boolean).join(' ')} {...rest}>
      {children}
    </td>
  )
}
