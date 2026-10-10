import { CaretRight } from '@phosphor-icons/react'
import { Chip } from '@heroui/react'
import { Link } from 'react-router-dom'

import { SectionCard } from '@/components/ui/SectionCard'
import { StatusDot } from '@/components/ui/StatusDot'
import { RankListSkeleton } from '@/components/ui/skeletons'
import { useI18n } from '@/hooks/I18nContext'

import type { InboxEvent } from './inboxEvents'

/** 收件箱可视行数上限（保持「一屏判健康」；其余经脚注指向账号池 / 福利）。 */
export const MAX_INBOX_ROWS = 8

/**
 * 待办收件箱（工作台第一公民，设计 D16）：按紧急度排序的事件列表，
 * 每条一行有效信息 + 时间，点击跳转处置处并高亮目标行（接收端负责）。
 * 头部右槽给出计数与「按紧急度排序」提示。
 */
export function Inbox({ events, loading }: { events: InboxEvent[]; loading: boolean }) {
  const { t } = useI18n()
  const visible = events.slice(0, MAX_INBOX_ROWS)
  const hiddenCount = events.length - visible.length

  return (
    <div data-gsap-reveal>
      <SectionCard
        padded={false}
        title={t('inboxTitle')}
        right={(
          <>
            <Chip size="sm" variant="soft">{t('inboxCount', { n: events.length })}</Chip>
            <span className="hidden text-xs text-muted sm:inline">{t('inboxHint')}</span>
          </>
        )}
      >
        {loading && events.length === 0 ? (
          <RankListSkeleton rows={4} />
        ) : events.length === 0 ? (
          <div className="wb-inbox-empty">{t('inboxEmpty')}</div>
        ) : (
          <div className="wb-inbox">
            {visible.map((event) => (
              <Link key={event.id} to={event.to} className="wb-inbox-item">
                <StatusDot state={event.severity === 'danger' ? 'danger' : 'warn'} />
                <span className="txt truncate">
                  <b>{event.title}</b>
                  {event.providerLabel ? (<><span className="sep">·</span><span className="provider">{event.providerLabel}</span></>) : null}
                  <span className="sep">·</span>
                  {event.detail}
                </span>
                {event.meta ? <span className="meta mono">{event.meta}</span> : null}
                <span className="go"><CaretRight size={14} /></span>
              </Link>
            ))}
          </div>
        )}
        {events.length > 0 ? (
          <div className="wb-inbox-foot">
            {hiddenCount > 0 ? `${t('inboxMore', { n: hiddenCount })} · ${t('inboxFoot')}` : t('inboxFoot')}
          </div>
        ) : null}
      </SectionCard>
    </div>
  )
}
