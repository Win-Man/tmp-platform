<template>
  <div class="page">
    <PageHeader title="审计日志" desc="记录登录、变更、导出等敏感操作，支持按条件检索与 CSV 导出">
      <template #actions>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button type="primary" plain :loading="exporting" @click="exportCsv">导出 CSV</el-button>
      </template>
    </PageHeader>

    <el-card shadow="never">
      <div class="table-toolbar">
        <div class="toolbar-left">
          <el-input
            v-model="filters.username"
            placeholder="操作者"
            clearable
            style="width: 160px"
            @keyup.enter="search"
            @clear="search"
          />
          <el-input
            v-model="filters.action"
            placeholder="操作，如 cluster.takeover"
            clearable
            style="width: 220px"
            @keyup.enter="search"
            @clear="search"
          />
          <el-select v-model="filters.result" placeholder="结果" clearable style="width: 120px" @change="search">
            <el-option label="成功" value="success" />
            <el-option label="失败" value="failed" />
          </el-select>
          <el-date-picker
            v-model="dateRange"
            type="daterange"
            range-separator="至"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            value-format="YYYY-MM-DD"
            style="width: 250px"
            @change="search"
          />
          <el-button @click="search">查询</el-button>
        </div>
        <div class="toolbar-right text-muted">共 {{ total }} 条</div>
      </div>

      <el-table v-loading="loading" :data="rows" size="small" empty-text="暂无审计记录">
        <el-table-column label="时间" width="160">
          <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column prop="username" label="操作者" width="130">
          <template #default="{ row }">{{ row.username || '—' }}</template>
        </el-table-column>
        <el-table-column prop="role" label="角色" width="90" />
        <el-table-column prop="action" label="操作" width="200">
          <template #default="{ row }"><span class="mono">{{ row.action }}</span></template>
        </el-table-column>
        <el-table-column label="对象" min-width="160">
          <template #default="{ row }">
            <span class="mono">{{ row.resourceType || '—' }}</span>
            <span v-if="row.resourceName" class="text-muted"> · {{ row.resourceName }}</span>
          </template>
        </el-table-column>
        <el-table-column label="结果" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.result === 'success' ? 'success' : 'danger'" effect="plain">
              {{ row.result === 'success' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="ip" label="来源 IP" width="130" />
        <el-table-column label="详情" min-width="200">
          <template #default="{ row }">
            <span class="mono text-muted detail-text" @click="openDetail(row)">{{ row.detail || '—' }}</span>
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

    <el-drawer v-model="detailVisible" title="审计详情" size="520px">
      <div v-if="current">
        <div class="detail-grid">
          <div class="detail-item"><span class="detail-label">时间</span><span class="detail-value">{{ formatTime(current.createdAt) }}</span></div>
          <div class="detail-item"><span class="detail-label">操作者</span><span class="detail-value">{{ current.username || '—' }}</span></div>
          <div class="detail-item"><span class="detail-label">操作</span><span class="detail-value mono">{{ current.action }}</span></div>
          <div class="detail-item"><span class="detail-label">结果</span><span class="detail-value">{{ current.result }}</span></div>
          <div class="detail-item"><span class="detail-label">资源</span><span class="detail-value">{{ current.resourceType }} {{ current.resourceId }}</span></div>
          <div class="detail-item"><span class="detail-label">IP</span><span class="detail-value mono">{{ current.ip }}</span></div>
          <div class="detail-item"><span class="detail-label">RequestId</span><span class="detail-value mono">{{ current.requestId }}</span></div>
          <div class="detail-item"><span class="detail-label">User-Agent</span><span class="detail-value mono">{{ current.userAgent }}</span></div>
        </div>
        <h4 style="margin: 16px 0 8px">详情（密码类字段已脱敏）</h4>
        <pre class="mono detail-json">{{ prettyDetail(current.detail) }}</pre>
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import { auditApi, downloadFile } from '@/api'
import type { AuditLogRow } from '@/api/types'
import { formatTime } from '@/utils/format'

const loading = ref(false)
const exporting = ref(false)
const rows = ref<AuditLogRow[]>([])
const total = ref(0)
const dateRange = ref<[string, string] | null>(null)
const detailVisible = ref(false)
const current = ref<AuditLogRow | null>(null)

const filters = reactive({ username: '', action: '', result: '', page: 1, pageSize: 20 })

function params(): Record<string, unknown> {
  return {
    page: filters.page,
    pageSize: filters.pageSize,
    username: filters.username || undefined,
    action: filters.action || undefined,
    result: filters.result || undefined,
    from: dateRange.value?.[0] || undefined,
    to: dateRange.value?.[1] || undefined,
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await auditApi.list(params())
    rows.value = res.list || []
    total.value = res.total || 0
  } finally {
    loading.value = false
  }
}

function search(): void {
  filters.page = 1
  void load()
}

function onPageChange(page: number): void {
  filters.page = page
  void load()
}

function onSizeChange(size: number): void {
  filters.pageSize = size
  filters.page = 1
  void load()
}

function openDetail(row: AuditLogRow): void {
  current.value = row
  detailVisible.value = true
}

function prettyDetail(detail?: string): string {
  if (!detail) {
    return '—'
  }
  try {
    return JSON.stringify(JSON.parse(detail), null, 2)
  } catch {
    return detail
  }
}

async function exportCsv(): Promise<void> {
  exporting.value = true
  try {
    const query = new URLSearchParams()
    Object.entries(params()).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        query.append(key, String(value))
      }
    })
    await downloadFile(`${auditApi.exportUrl}?${query.toString()}`, 'audit-logs.csv')
    ElMessage.success('导出已开始')
  } finally {
    exporting.value = false
  }
}

onMounted(() => {
  void load()
})
</script>

<style scoped lang="scss">
.detail-text {
  cursor: pointer;
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-json {
  background: #0f172a;
  color: #cbd5e1;
  padding: 12px;
  border-radius: 6px;
  font-size: 12.5px;
  max-height: 420px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
