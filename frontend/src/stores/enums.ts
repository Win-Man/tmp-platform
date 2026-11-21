import { defineStore } from 'pinia'
import { ref } from 'vue'
import { metaApi } from '@/api'
import type { EnumDict, EnumOption } from '@/api/types'

/** 枚举字典：一次拉取，全站复用（下拉、状态文案、颜色）。 */
export const useEnumStore = defineStore('enums', () => {
  const dict = ref<EnumDict | null>(null)
  const loading = ref(false)

  /** 加载枚举（幂等，失败不阻塞页面）。 */
  async function ensure(): Promise<EnumDict | null> {
    if (dict.value || loading.value) {
      return dict.value
    }
    loading.value = true
    try {
      dict.value = await metaApi.enums()
    } catch {
      dict.value = null
    } finally {
      loading.value = false
    }
    return dict.value
  }

  /** 取某组枚举。 */
  function group(key: keyof EnumDict): EnumOption[] {
    const value = dict.value?.[key]
    return Array.isArray(value) ? (value as EnumOption[]) : []
  }

  /** 枚举值转文案。 */
  function label(key: keyof EnumDict, value: string): string {
    const hit = group(key).find((o) => o.value === value)
    return hit ? hit.label : value
  }

  return { dict, loading, ensure, group, label }
})

/** 集群健康状态 → Element Plus tag 类型。 */
export function healthTagType(status: string): 'success' | 'warning' | 'danger' | 'info' {
  switch (status) {
    case 'healthy':
      return 'success'
    case 'degraded':
      return 'warning'
    case 'error':
      return 'danger'
    default:
      return 'info'
  }
}

/** 任务/步骤状态 → tag 类型。 */
export function jobTagType(status: string): 'success' | 'warning' | 'danger' | 'info' | 'primary' {
  switch (status) {
    case 'success':
      return 'success'
    case 'running':
      return 'primary'
    case 'pending':
      return 'warning'
    case 'failed':
      return 'danger'
    case 'canceled':
      return 'info'
    case 'interrupted':
      return 'danger'
    default:
      return 'info'
  }
}

/** 巡检项级别 → tag 类型。 */
export function levelTagType(level: string): 'success' | 'warning' | 'danger' | 'info' {
  switch (level) {
    case 'critical':
      return 'danger'
    case 'warning':
      return 'warning'
    case 'pass':
      return 'success'
    default:
      return 'info'
  }
}
