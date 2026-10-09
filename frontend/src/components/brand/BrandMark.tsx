type Props = {
  size?: number
  className?: string
  /** 沿品牌导轨的青色扫描。用于进行中的品牌加载，而非页面骨架屏。 */
  loading?: boolean
}

const RAIL = 'M 30 64 L 40 64 M 64 40 L 40 64 L 64 88'
const TIP = 'M 88 64 L 98 64 M 64 40 L 88 64 L 64 88'

/** 应用品牌标识（几何与 /favicon.svg 相同）。描边跟随主题
   墨色；青色尖端固定，作为品牌点缀。 */
export function BrandMark({ size = 24, className = '', loading = false }: Props) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="22 22 84 84"
      fill="none"
      aria-hidden="true"
      className={`brand-mark block shrink-0 text-foreground ${loading ? 'brand-mark--loading' : ''} ${className}`.trim()}
    >
      <path
        className="brand-mark__rail"
        pathLength={100}
        d={RAIL}
        stroke="currentColor"
        strokeWidth={11.4}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        className="brand-mark__tip"
        pathLength={100}
        d={TIP}
        stroke="#22D3EE"
        strokeWidth={11.4}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {loading ? (
        <path
          className="brand-mark__scan"
          pathLength={100}
          d={RAIL}
          stroke="#22D3EE"
          strokeWidth={11.4}
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      ) : null}
    </svg>
  )
}
