<template>
  <span class="status-tag">
    <span class="status-dot" :class="dotClass" />
    <span>{{ text }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useEnumStore } from '@/stores/enums'
import type { EnumDict } from '@/api/types'

/**
 * 状态展示：色点 + 文案（不使用纯色块，保证可读性与一致性）。
 * kind 决定文案字典与配色规则。
 */
const props = withDefaults(
  defineProps<{
    status: string
    kind?: 'cluster' | 'collect' | 'job' | 'level' | 'instance' | 'ssh' | 'conclusion'
  }>(),
  { kind: 'cluster' },
)

const enums = useEnumStore()

const dictKey = computed<keyof EnumDict>(() => {
  switch (props.kind) {
    case 'collect':
      return 'collectStatus'
    case 'job':
      return 'jobStatus'
    case 'level':
      return 'inspectLevel'
    case 'instance':
      return 'instanceStatus'
    default:
      return 'clusterStatus'
  }
})

const text = computed(() => {
  const status = props.status || 'unknown'
  if (props.kind === 'ssh') {
    return { unknown: '未检测', success: '通', failed: '不通', error: '不通' }[status] || status
  }
  if (props.kind === 'conclusion') {
    return { pass: '通过', risk: '存在风险', fail: '不通过' }[status] || status
  }
  return enums.label(dictKey.value, status)
})

const dotClass = computed(() => {
  const status = props.status
  if (props.kind === 'ssh') {
    if (status === 'success') return 'dot-success'
    if (status === 'unknown' || !status) return 'dot-info'
    return 'dot-danger'
  }
  if (props.kind === 'conclusion') {
    if (status === 'pass') return 'dot-success'
    if (status === 'risk') return 'dot-warning'
    return 'dot-danger'
  }
  switch (status) {
    case 'healthy':
    case 'success':
    case 'pass':
    case 'up':
    case 'ready':
      return 'dot-success'
    case 'degraded':
    case 'warning':
    case 'pending':
    case 'unhealthy':
      return 'dot-warning'
    case 'error':
    case 'failed':
    case 'critical':
    case 'down':
    case 'interrupted':
      return 'dot-danger'
    case 'running':
      return 'dot-warning'
    default:
      return 'dot-info'
  }
})
</script>

<style scoped>
.status-tag {
  display: inline-flex;
  align-items: center;
  font-size: 13px;
  white-space: nowrap;
}
</style>
