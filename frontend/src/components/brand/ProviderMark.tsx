import { TraeMark } from '@/components/brand/TraeMark'
import { WorkBuddyMark } from '@/components/brand/WorkBuddyMark'

type Props = {
  provider?: string
  size?: number
  className?: string
}

/** 账号 provider 的品牌标识；未知取值不渲染任何标识。 */
export function ProviderMark({ provider, size = 16, className = '' }: Props) {
  const id = String(provider || '').toLowerCase()
  if (id === 'workbuddy') {
    return <WorkBuddyMark size={size} className={className} />
  }
  if (id === 'trae') {
    return <TraeMark size={size} className={className} />
  }
  return null
}
