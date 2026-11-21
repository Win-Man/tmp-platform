import type { LocationQuery, Router } from 'vue-router'

/** 允许写入 URL 的查询值类型。 */
export type QueryValue = string | number | boolean | undefined | null

/** 读取字符串 query 参数。 */
export function queryString(query: LocationQuery, key: string): string {
  const value = query[key]
  if (typeof value === 'string') {
    return value
  }
  if (Array.isArray(value) && typeof value[0] === 'string') {
    return value[0]
  }
  return ''
}

/** 读取正整数 query 参数（非法或缺省时返回 fallback）。 */
export function queryInt(query: LocationQuery, key: string, fallback: number): number {
  const raw = queryString(query, key)
  const value = Number(raw)
  if (!Number.isFinite(value) || value <= 0) {
    return fallback
  }
  return Math.floor(value)
}

/** 读取布尔 query 参数（"1" / "true" 视为真）。 */
export function queryBool(query: LocationQuery, key: string): boolean {
  const raw = queryString(query, key).toLowerCase()
  return raw === '1' || raw === 'true'
}

/**
 * 把筛选条件同步到 URL query（replace 不产生历史记录）。
 *
 * 与当前 query 完全一致时直接返回，避免「同步 → 路由变化 → 重新加载」的循环。
 */
export function syncQuery(router: Router, current: LocationQuery, values: Record<string, QueryValue>): void {
  const next: Record<string, string> = {}
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null || value === '') {
      continue
    }
    next[key] = String(value)
  }
  const currentKeys = Object.keys(current).filter((k) => current[k] !== undefined && current[k] !== '')
  if (currentKeys.length === Object.keys(next).length) {
    let same = true
    for (const key of currentKeys) {
      if (queryString(current, key) !== next[key]) {
        same = false
        break
      }
    }
    if (same) {
      return
    }
  }
  void router.replace({ query: next })
}
