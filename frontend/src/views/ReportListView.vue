<template>
  <div class="page">
    <PageHeader title="巡检报告" desc="按集群与时间归档的巡检结论，支持原文渲染与 Markdown / HTML 下载">
      <template #actions>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
      </template>
    </PageHeader>

    <el-card shadow="never">
      <div class="table-toolbar">
        <div class="toolbar-left">
          <el-select v-model="filters.clusterId" placeholder="集群" clearable style="width: 200px" @change="search">
            <el-option v-for="c in clusters" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
          <el-date-picker
            v-model="dateRange"
            type="daterange"
            range-separator="至"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            value-format="YYYY-MM-DD"
            style="width: 260px"
            @change="search"
          />
          <el-button @click="search">查询</el-button>
        </div>
        <div class="toolbar-right text-muted">共 {{ total }} 份报告</div>
      </div>

      <el-alert
        v-if="errorMessage"
        type="error"
        show-icon
        :closable="false"
        class="mb-12"
        :title="`报告列表加载失败：${errorMessage}`"
      >
        <el-button size="small" @click="load">重试</el-button>
      </el-alert>

      <el-table v-loading="loading" :data="rows" size="small" empty-text="暂无巡检报告，可在巡检中心发起巡检">
        <el-table-column prop="reportNo" label="报告号" width="160" />
        <el-table-column label="集群" min-width="140">
          <template #default="{ row }">
            {{ row.clusterName }}
            <el-tag v-if="row.clusterRemoved" size="small" type="info" effect="plain">已移除</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="templateName" label="模板" width="150" />
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
        <el-table-column label="执行时间" width="160">
          <template #default="{ row }">{{ formatTime(row.executedAt, 'MM-DD HH:mm:ss') }}</template>
        </el-table-column>
        <el-table-column label="大小" width="100">
          <template #default="{ row }">{{ formatBytes(row.fileSize) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="router.push(`/reports/${row.id}`)">查看</el-button>
            <el-button v-if="auth.can('report.download')" link type="primary" @click="download(row, 'md')">MD</el-button>
            <el-button v-if="auth.can('report.download')" link type="primary" @click="download(row, 'html')">
              HTML
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
        :page-sizes="[10, 20, 50]"
        @size-change="onSizeChange"
        @current-change="onPageChange"
      />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import { clusterApi, downloadFile, inspectApi } from '@/api'
import type { ClusterOption, InspectReport } from '@/api/types'
import { formatBytes, formatTime } from '@/utils/format'
import { queryInt, queryString, syncQuery } from '@/utils/query'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const loading = ref(false)
const rows = ref<InspectReport[]>([])
const total = ref(0)
const clusters = ref<ClusterOption[]>([])
const dateRange = ref<[string, string] | null>(null)
const errorMessage = ref('')

const filters = reactive({ clusterId: undefined as number | undefined, page: 1, pageSize: 10 })

/** 把筛选与分页写入 URL query。 */
function syncUrl(): void {
  syncQuery(router, route.query, {
    clusterId: filters.clusterId,
    from: dateRange.value?.[0],
    to: dateRange.value?.[1],
    page: filters.page > 1 ? filters.page : undefined,
    pageSize: filters.pageSize !== 10 ? filters.pageSize : undefined,
  })
}

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await inspectApi.reports({
      page: filters.page,
      pageSize: filters.pageSize,
      clusterId: filters.clusterId || undefined,
      from: dateRange.value?.[0] || undefined,
      to: dateRange.value?.[1] || undefined,
    })
    rows.value = res.list || []
    total.value = res.total || 0
  } catch (err) {
    rows.value = []
    total.value = 0
    errorMessage.value = err instanceof Error ? err.message : '请求失败'
  } finally {
    loading.value = false
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

async function download(row: InspectReport, format: 'md' | 'html'): Promise<void> {
  await downloadFile(inspectApi.downloadUrl(row.id, format), `${row.reportNo}.${format}`)
  ElMessage.success('下载已开始')
}

onMounted(async () => {
  // 从 URL 恢复筛选（刷新 / 分享链接）。
  const clusterIdParam = queryInt(route.query, 'clusterId', 0)
  filters.clusterId = clusterIdParam > 0 ? clusterIdParam : undefined
  const from = queryString(route.query, 'from')
  const to = queryString(route.query, 'to')
  dateRange.value = from && to ? [from, to] : null
  filters.page = queryInt(route.query, 'page', 1)
  filters.pageSize = queryInt(route.query, 'pageSize', 10)
  clusters.value = await clusterApi.options().catch(() => [])
  await load()
})
</script>
