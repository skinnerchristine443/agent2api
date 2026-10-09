type Item<T extends string> = {
  id: T
  label: string
  /** 可选的计数徽标（如「需关注 3」）。 */
  count?: number
}

type Props<T extends string> = {
  items: ReadonlyArray<Item<T>>
  value: T
  onChange: (id: T) => void
  ariaLabel: string
}

/** 页内分段控件（设计 §6.1：分段 ≤3、不占菜单层级；激活态 accent-soft + accent 字）。 */
export function Segmented<T extends string>({ items, value, onChange, ariaLabel }: Props<T>) {
  return (
    <div className="seg" role="tablist" aria-label={ariaLabel}>
      {items.map((item) => {
        const active = value === item.id
        return (
          <button
            key={item.id}
            type="button"
            role="tab"
            aria-selected={active}
            className={`seg-item${active ? ' active' : ''}`}
            onClick={() => onChange(item.id)}
          >
            {item.label}
            {item.count !== undefined ? <span className="seg-count mono">{item.count}</span> : null}
          </button>
        )
      })}
    </div>
  )
}
