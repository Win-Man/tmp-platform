<template>
  <div v-loading="loading">
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-button :icon="Refresh" @click="load">刷新</el-button>
        <el-button
          v-if="auth.can('inspect.run')"
          type="primary"
          :icon="Aim"
          @click="goInspect"
        >
          为本集群发起巡检
        </el-button>
      </div>
      <div class="toolbar-right text-muted">共 {{ total }} 份报告</div>
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

    <el-table :data="rows" size="small" empty-text="该集群暂无巡检报告">
      <el-table-column prop="reportNo" label="报告号" width="170" />
      <el-table-column label="模板" width="150" prop="templateName" />
      <el-table-column label="结论" width="110">
        <template #default="{ row }"><StatusTag :status="row.conclusion" kind="conclusion" /></template>
      </el-table-column>
      <el-table-column label="通过率" width="100">
        <template #default="{ row }">{{ row.passRate }}%</template>
      </el-table-column>
      <el-table-column label="严重/警告/通过" width="150">
        <template #default="{ row }">
          <span class="text-muted">{{ row.criticalCount }} / {{ row.warningCount }} / {{ row.passCount }}</span>
        </template>
      </el-table-column>
      <el-table-column label="执行时间" width="170">
        <template #default="{ row }">{{ formatTime(row.executedAt, 'MM-DD HH:mm:ss') }}</template>
      </el-table-column>
      <el-table-column label="操作" width="160" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="router.push(`/reports/${row.id}`)">查看</el-button>
          <el-button
            v-if="auth.can('report.download')"
            link
            type="primary"
            @click="download(row)"
          >
            MD
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      class="mt-12"
      layout="total, prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      @current-change="onPageChange"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Aim, Refresh } from '@element-plus/icons-vue'
import StatusTag from '@/components/StatusTag.vue'
import { downloadFile, inspectApi } from '@/api'
import type { InspectReport } from '@/api/types'
import { formatTime } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ clusterId: number }>()

const router = useRouter()
const auth = useAuthStore()

const loading = ref(false)
const rows = ref<InspectReport[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 10
const errorMessage = ref('')

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await inspectApi.reports({
      page: page.value,
      pageSize,
      clusterId: props.clusterId,
    })
    rows.value = res.list || []
    total.value = res.total || 0
  } catch (err) {
    rows.value = []
    total.value = 0
    errorMessage.value = err instanceof Error ? err.message : '加载巡检报告失败'
  } finally {
    loading.value = false
  }
}

function onPageChange(next: number): void {
  page.value = next
  void load()
}

function goInspect(): void {
  void router.push({ name: 'inspection', query: { clusterId: String(props.clusterId) } })
}

async function download(row: InspectReport): Promise<void> {
  await downloadFile(inspectApi.downloadUrl(row.id, 'md'), `${row.reportNo}.md`)
  ElMessage.success('下载已开始')
}

onMounted(() => {
  void load()
})
</script>
