import { NavLink } from 'react-router-dom'

type Item = {
  to: string
  label: string
}

/**
 * 页内分段导航（链接版；规格同 `Segmented`——`.seg / .seg-item`）。
 * 用于**跨路径**页签（如观测三页），激活态按当前路由推导。
 */
export function SegmentedNav({ ariaLabel, items }: { ariaLabel: string; items: ReadonlyArray<Item> }) {
  return (
    <nav className="seg" role="tablist" aria-label={ariaLabel}>
      {items.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end
          role="tab"
          className={({ isActive }) => `seg-item${isActive ? ' active' : ''}`}
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  )
}
