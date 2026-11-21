import dayjs from 'dayjs'

/** 日期时间格式化（空值输出占位符）。 */
export function formatTime(value?: string | null, pattern = 'YYYY-MM-DD HH:mm:ss'): string {
  if (!value) {
    return '—'
  }
  const d = dayjs(value)
  return d.isValid() ? d.format(pattern) : '—'
}

/** 相对时间：刚刚 / N 分钟前 / N 小时前 / 日期。 */
export function fromNow(value?: string | null): string {
  if (!value) {
    return '—'
  }
  const d = dayjs(value)
  if (!d.isValid()) {
    return '—'
  }
  const diff = dayjs().diff(d, 'second')
  if (diff < 60) {
    return '刚刚'
  }
  if (diff < 3600) {
    return `${Math.floor(diff / 60)} 分钟前`
  }
  if (diff < 86400) {
    return `${Math.floor(diff / 3600)} 小时前`
  }
  return d.format('YYYY-MM-DD HH:mm')
}

/** 毫秒转可读时长。 */
export function formatDuration(ms?: number | null): string {
  if (ms === undefined || ms === null || ms < 0) {
    return '—'
  }
  if (ms < 1000) {
    return `${ms}ms`
  }
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) {
    return `${seconds}s`
  }
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) {
    return `${minutes}m${seconds % 60}s`
  }
  const hours = Math.floor(minutes / 60)
  return `${hours}h${minutes % 60}m`
}

/** 字节转可读大小。 */
export function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) {
    return '—'
  }
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

/** 使用率 → 进度条颜色。 */
export function usageColor(pct: number): string {
  if (pct >= 90) {
    return '#f56c6c'
  }
  if (pct >= 75) {
    return '#e6a23c'
  }
  return '#67c23a'
}

/** 数值保留一位小数（空值输出 —）。 */
export function fmtNum(value?: number | null, digits = 1): string {
  if (value === undefined || value === null || Number.isNaN(value)) {
    return '—'
  }
  return value.toFixed(digits)
}
