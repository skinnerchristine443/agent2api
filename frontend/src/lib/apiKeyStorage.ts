// 负责 console API key 的浏览器存储，使 React provider 与
// 非 React 调用方（api/client.ts）共用同一个 key 和同一条读取路径。
export const API_KEY_STORAGE_KEY = 'agent2api_key'

export function readStoredApiKey() {
  return (localStorage.getItem(API_KEY_STORAGE_KEY) || '').trim()
}
