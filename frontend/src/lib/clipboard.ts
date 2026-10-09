// copyText 将 `value` 复制到剪贴板，在异步 Clipboard API 不可用时回退到
// 隐藏的 textarea + execCommand。navigator.clipboard 仅在安全上下文中定义，
// 因此通过纯 http 提供的 console（如 http://192.168.101.1:3010）
// 否则会静默地复制失败。
export async function copyText(value: string): Promise<boolean> {
  if (!value) return false
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(value)
      return true
    }
  } catch {
    // 回退到旧版路径
  }
  try {
    const area = document.createElement('textarea')
    area.value = value
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.top = '-1000px'
    area.style.opacity = '0'
    document.body.appendChild(area)
    area.focus()
    area.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(area)
    return ok
  } catch {
    return false
  }
}
