import traeMark from '@/assets/trae-mark.svg'

type Props = {
  size?: number
  className?: string
}

export function TraeMark({ size = 16, className = '' }: Props) {
  return (
    <img
      src={traeMark}
      alt=""
      width={size}
      height={size}
      className={`block shrink-0 rounded-[22%] ${className}`.trim()}
      draggable={false}
    />
  )
}
