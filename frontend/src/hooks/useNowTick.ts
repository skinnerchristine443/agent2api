import { useEffect, useState } from 'react'

import { isPageVisible, subscribePageVisibility } from '@/lib/pageVisibility'

// 模块级共享单例：所有 useNowTick 实例共用同一个 1s 定时器与一份订阅集合，
// 避免「每张卡片各起一个定时器」。最后一个订阅者退订即停表；页面隐藏暂停、
// 恢复时立即补一次 tick，时间基准不跳变。
const subscribers = new Set<(now: number) => void>()
let timer: ReturnType<typeof setInterval> | null = null
let unsubscribeVisibility: (() => void) | null = null
let sharedNow = Date.now()

function tick() {
  sharedNow = Date.now()
  subscribers.forEach((notify) => notify(sharedNow))
}

function startTimer() {
  if (timer != null) return
  timer = setInterval(tick, 1000)
}

function stopTimer() {
  if (timer == null) return
  clearInterval(timer)
  timer = null
}

function onVisibilityChange(visible: boolean) {
  if (subscribers.size === 0) return
  if (visible) {
    tick()
    startTimer()
  } else {
    stopTimer()
  }
}

function subscribe(notify: (now: number) => void) {
  if (subscribers.size === 0) {
    sharedNow = Date.now() // 冷启动：对齐真实时间
    unsubscribeVisibility = subscribePageVisibility(onVisibilityChange)
    if (isPageVisible()) startTimer()
  }
  subscribers.add(notify)
  return () => {
    subscribers.delete(notify)
    if (subscribers.size > 0) return
    stopTimer()
    unsubscribeVisibility?.()
    unsubscribeVisibility = null
  }
}

/**
 * UI 时钟（倒计时 / 相对时间刷新）：返回当前时间戳，每 1s 推进一次。
 * active=false 即停（不订阅、不计时）；实例共享单例，故不会是「一卡一定时器」。
 */
export function useNowTick(active = true): number {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!active) return
    const unsubscribe = subscribe(setNow)
    setNow(sharedNow) // eslint-disable-line react/set-state-in-effect -- 订阅时对齐共享基准，避免多实例读数不一致
    return unsubscribe
  }, [active])

  return now
}
