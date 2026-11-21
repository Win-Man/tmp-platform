<template>
  <div v-loading="loading">
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-button :icon="Refresh" @click="load">刷新</el-button>
      </div>
      <div class="toolbar-right text-muted">
        共 {{ rows.length }} 个 Resource Group
      </div>
    </div>

    <el-alert
      v-if="errorMessage"
      type="error"
      show-icon
      :closable="false"
      class="mb-12"
      :title="errorMessage"
    >
      <el-button size="small" @click="load">重试</el-button>
    </el-alert>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      class="mb-12"
      title="Resource Group 的绑定 / 解绑入口在「TiDB 用户」页：选择用户后点击「绑定 RG」。"
    />

    <el-table :data="rows" size="small" empty-text="暂无 Resource Group 快照（需 v7.5+ 且已完成采集）">
      <el-table-column label="名称" min-width="160">
        <template #default="{ row }"><span class="mono">{{ row.name }}</span></template>
      </el-table-column>
      <el-table-column label="RU/秒" width="120">
        <template #default="{ row }">{{ row.ruPerSec ?? '—' }}</template>
      </el-table-column>
      <el-table-column label="Burstable" width="120">
        <template #default="{ row }">
          <el-tag :type="row.burstable ? 'success' : 'info'" size="small" effect="plain">
            {{ row.burstable ? '是' : '否' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="采集时间" width="170">
        <template #default="{ row }">{{ formatTime(row.fetchedAt, 'MM-DD HH:mm:ss') }}</template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { tidbApi } from '@/api'
import type { ResourceGroupRow } from '@/api/types'
import { formatTime } from '@/utils/format'

const props = defineProps<{ clusterId: number }>()

const loading = ref(false)
const rows = ref<ResourceGroupRow[]>([])
const errorMessage = ref('')

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await tidbApi.resourceGroups(props.clusterId)
    rows.value = res.list || []
  } catch (err) {
    rows.value = []
    errorMessage.value = err instanceof Error ? err.message : '加载 Resource Group 失败'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})
</script>
