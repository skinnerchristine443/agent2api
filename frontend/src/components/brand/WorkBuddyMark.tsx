import workBuddyMark from '@/assets/workbuddy-mark.svg'

type Props = {
  size?: number
  className?: string
}

export function WorkBuddyMark({ size = 16, className = '' }: Props) {
  return (
    <img
      src={workBuddyMark}
      alt=""
      width={size}
      height={size}
      className={`block shrink-0 rounded-[22%] ${className}`.trim()}
      draggable={false}
    />
  )
}
