<template>
  <div class="probe-list">
    <div v-for="item in results" :key="item.scope" class="probe-item">
      <div class="probe-head">
        <span class="status-dot" :class="item.ok ? 'dot-success' : 'dot-danger'" />
        <strong>{{ scopeLabel(item.scope) }}</strong>
        <el-tag size="small" :type="item.ok ? 'success' : 'danger'" effect="plain">
          {{ item.ok ? '连通' : '失败' }}
        </el-tag>
        <span class="text-muted">{{ item.latencyMs }}ms</span>
      </div>
      <div class="probe-msg mono">{{ item.message || '—' }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { ProbeResult } from '@/api/types'

defineProps<{ results: ProbeResult[] }>()

/** 通道名称中文映射。 */
function scopeLabel(scope: string): string {
  return { ssh: 'SSH（OS 通道）', mysql: '数据库（MySQL 协议）', pd: 'PD（HTTP API）' }[scope] || scope
}
</script>

<style scoped>
.probe-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.probe-item {
  border: 1px solid var(--tmp-border);
  border-radius: 6px;
  padding: 10px 12px;
  background: #fafbfc;
}

.probe-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.probe-msg {
  margin-top: 6px;
  color: var(--tmp-text-muted);
  word-break: break-all;
}
</style>
