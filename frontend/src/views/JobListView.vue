<template>
  <div class="page">
    <PageHeader title="任务中心" desc="所有异步操作的统一视图：进度、步骤与日志">
      <template #actions>
        <el-switch v-model="autoRefresh" active-text="自动刷新" style="margin-right: 8px" />
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
      </template>
    </PageHeader>

    <el-card shadow="never">
      <div class="table-toolbar">
        <div class="toolbar-left">
          <el-select v-model="filters.type" placeholder="任务类型" clearable style="width: 170px" @change="search">
            <el-option v-for="o in enums.group('jobType')" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <el-select v-model="filters.status" placeholder="状态" clearable style="width: 140px" @change="search">
            <el-option v-for="o in enums.group('jobStatus')" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <el-select v-model="filters.clusterId" placeholder="集群" clearable style="width: 180px" @change="search">
            <el-option v-for="c in clusters" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
          <el-checkbox v-model="filters.mine" @change="search">只看我的</el-checkbox>
          <el-button @click="search">查询</el-button>
        </div>
        <div class="toolbar-right text-muted">共 {{ total }} 个任务</div>
      </div>

      <el-alert
        v-if="errorMessage"
        type="error"
        show-icon
        :closable="false"
        class="mb-12"
        :title="`任务列表加载失败：${errorMessage}`"
      >
        <el-button size="small" @click="load()">重试</el-button>
      </el-alert>

      <el-table v-loading="loading" :data="rows" size="small" empty-text="暂无任务记录">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="类型" width="140">
          <template #default="{ row }">{{ row.typeLabel || enums.label('jobType', row.type) }}</template>
        </el-table-column>
        <el-table-column label="对象" min-width="150">
          <template #default="{ row }">
            <router-link v-if="row.clusterId" :to="`/clusters/${row.clusterId}`" class="link">
              {{ row.targetLabel || `集群 #${row.clusterId}` }}
            </router-link>
            <span v-else>{{ row.targetLabel || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }"><StatusTag :status="row.status" kind="job" /></template>
        </el-table-column>
        <el-table-column label="进度" width="160">
          <template #default="{ row }">
            <el-progress
              :percentage="row.progress"
              :stroke-width="8"
              :status="row.status === 'failed' ? 'exception' : row.status === 'success' ? 'success' : undefined"
            />
            <div class="text-muted" style="font-size: 12px">{{ row.currentStep || '—' }}</div>
          </template>
        </el-table-column>
        <el-table-column label="发起人" width="110">
          <template #default="{ row }">{{ row.createdByName || '—' }}</template>
        </el-table-column>
        <el-table-column label="耗时" width="90">
          <template #default="{ row }">{{ formatDuration(row.durationMs) }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="150">
          <template #default="{ row }">{{ formatTime(row.createdAt, 'MM-DD HH:mm:ss') }}</template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="router.push(`/jobs/${row.id}`)">详情</el-button>
            <el-button
              v-if="auth.can('job.retry') && ['failed', 'interrupted', 'canceled'].includes(row.status)"
              link
              type="primary"
              @click="retry(row)"
            >
              重试
            </el-button>
            <el-button
              v-if="auth.can('job.cancel') && ['pending', 'running'].includes(row.status)"
              link
              type="danger"
              @click="cancel(row)"
            >
              取消
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        class="mt-12"
        layout="total, sizes, prev, pager, next"
        :total="total"
        :page-size="filters.pageSize"
        :current-page="filters.page"
        :page-sizes="[20, 50, 100]"
        @size-change="onSizeChange"
        @current-change="onPageChange"
      />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import { clusterApi, jobApi } from '@/api'
import type { ClusterOption, JobRow } from '@/api/types'
import { formatDuration, formatTime } from '@/utils/format'
import { queryBool, queryInt, queryString, syncQuery } from '@/utils/query'
import { useAuthStore } from '@/stores/auth'
import { useEnumStore } from '@/stores/enums'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const enums = useEnumStore()

const loading = ref(false)
const rows = ref<JobRow[]>([])
const total = ref(0)
const clusters = ref<ClusterOption[]>([])
const autoRefresh = ref(true)
const errorMessage = ref('')
let timer: number | null = null

const filters = reactive({
  type: '',
  status: '',
  clusterId: undefined as number | undefined,
  mine: false,
  page: 1,
  pageSize: 20,
})

/** 把筛选与分页写入 URL query。 */
function syncUrl(): void {
  syncQuery(router, route.query, {
    type: filters.type,
    status: filters.status,
    clusterId: filters.clusterId,
    mine: filters.mine ? 1 : undefined,
    page: filters.page > 1 ? filters.page : undefined,
    pageSize: filters.pageSize !== 20 ? filters.pageSize : undefined,
  })
}

async function load(silent = false): Promise<void> {
  if (!silent) {
    loading.value = true
    errorMessage.value = ''
  }
  try {
    const res = await jobApi.list({
      page: filters.page,
      pageSize: filters.pageSize,
      type: filters.type || undefined,
      status: filters.status || undefined,
      clusterId: filters.clusterId || undefined,
      mine: filters.mine || undefined,
    })
    rows.value = res.list || []
    total.value = res.total || 0
  } catch (err) {
    if (!silent) {
      rows.value = []
      total.value = 0
      errorMessage.value = err instanceof Error ? err.message : '请求失败'
    }
  } finally {
    if (!silent) {
      loading.value = false
    }
  }
}

function search(): void {
  filters.page = 1
  syncUrl()
  void load()
}

function onPageChange(page: number): void {
  filters.page = page
  syncUrl()
  void load()
}

function onSizeChange(size: number): void {
  filters.pageSize = size
  filters.page = 1
  syncUrl()
  void load()
}

async function retry(row: JobRow): Promise<void> {
  const res = await jobApi.retry(row.id)
  ElMessage.success(`重试任务 #${res.jobId} 已创建`)
  void router.push(`/jobs/${res.jobId}`)
}

async function cancel(row: JobRow): Promise<void> {
  const ok = await ElMessageBox.confirm(`确认取消任务 #${row.id}？`, '取消任务', { type: 'warning' }).catch(
    () => false,
  )
  if (!ok) {
    return
  }
  await jobApi.cancel(row.id)
  ElMessage.success('已请求取消，任务将在当前步骤结束后停止')
  await load()
}

function startTimer(): void {
  stopTimer()
  timer = window.setInterval(() => {
    const hasActive = rows.value.some((r) => r.status === 'pending' || r.status === 'running')
    if (hasActive) {
      void load(true)
    }
  }, 5000)
}

function stopTimer(): void {
  if (timer !== null) {
    window.clearInterval(timer)
    timer = null
  }
}

watch(autoRefresh, (v) => {
  if (v) {
    startTimer()
  } else {
    stopTimer()
  }
})

onMounted(async () => {
  await enums.ensure()
  // 从 URL 恢复筛选（刷新 / 分享链接）。
  filters.type = queryString(route.query, 'type')
  filters.status = queryString(route.query, 'status')
  const clusterIdParam = queryInt(route.query, 'clusterId', 0)
  filters.clusterId = clusterIdParam > 0 ? clusterIdParam : undefined
  filters.mine = queryBool(route.query, 'mine')
  filters.page = queryInt(route.query, 'page', 1)
  filters.pageSize = queryInt(route.query, 'pageSize', 20)
  clusters.value = await clusterApi.options().catch(() => [])
  await load()
  startTimer()
})

onBeforeUnmount(() => {
  stopTimer()
})
</script>

<style scoped lang="scss">
.link {
  color: #2f6fed;
  text-decoration: none;
}
</style>
