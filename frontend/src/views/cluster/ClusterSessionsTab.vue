<template>
  <div>
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-input
          v-model="keyword"
          placeholder="用户 / 库 / SQL 关键词"
          clearable
          style="width: 220px"
          :prefix-icon="Search"
          @keyup.enter="search"
          @clear="search"
        />
        <el-select v-model="sortBy" style="width: 150px" @change="search">
          <el-option label="按执行时间" value="time" />
          <el-option label="按会话 ID" value="id" />
        </el-select>
        <el-button @click="search">查询</el-button>
      </div>
      <div class="toolbar-right">
        <span class="text-muted" style="margin-right: 8px">共 {{ total }} 个会话</span>
        <el-button
          v-if="auth.can('session.kill')"
          type="danger"
          :disabled="selection.length === 0"
          @click="killSelected"
        >
          Kill 选中（{{ selection.length }}）
        </el-button>
      </div>
    </div>

    <el-table
      v-loading="loading"
      :data="rows"
      size="small"
      empty-text="暂无会话数据"
      @selection-change="onSelectionChange"
    >
      <el-table-column type="selection" width="44" />
      <el-table-column prop="sessionId" label="会话 ID" width="100" />
      <el-table-column prop="userName" label="用户" width="130" />
      <el-table-column prop="host" label="来源" width="160">
        <template #default="{ row }"><span class="mono">{{ row.host }}</span></template>
      </el-table-column>
      <el-table-column prop="db" label="数据库" width="120">
        <template #default="{ row }">{{ row.db || '—' }}</template>
      </el-table-column>
      <el-table-column prop="command" label="命令" width="100" />
      <el-table-column label="执行时长" width="110">
        <template #default="{ row }">
          <el-tag v-if="row.timeSec >= 60" size="small" type="warning" effect="plain">{{ row.timeSec }}s</el-tag>
          <span v-else>{{ row.timeSec }}s</span>
        </template>
      </el-table-column>
      <el-table-column prop="state" label="状态" width="130" />
      <el-table-column label="SQL" min-width="280">
        <template #default="{ row }"><span class="mono">{{ row.sqlDigest || '—' }}</span></template>
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
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { tidbApi } from '@/api'
import type { SessionRow } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ clusterId: number }>()

const auth = useAuthStore()
const loading = ref(false)
const rows = ref<SessionRow[]>([])
const total = ref(0)
const keyword = ref('')
const sortBy = ref('time')
const page = ref(1)
const pageSize = ref(20)
const selection = ref<SessionRow[]>([])

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await tidbApi.sessions(props.clusterId, {
      page: page.value,
      pageSize: pageSize.value,
      keyword: keyword.value || undefined,
      sortBy: sortBy.value,
    })
    rows.value = res.list || []
    total.value = res.total || 0
  } finally {
    loading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function onPageChange(p: number): void {
  page.value = p
  void load()
}

function onSelectionChange(val: SessionRow[]): void {
  selection.value = val
}

async function killSelected(): Promise<void> {
  const ids = selection.value.map((s) => s.sessionId)
  const ok = await ElMessageBox.confirm(
    `确认 Kill ${ids.length} 个会话？操作会立即中断对应查询。`,
    'Kill 会话',
    { type: 'warning' },
  ).catch(() => false)
  if (!ok) {
    return
  }
  const res = await tidbApi.killSessions(props.clusterId, ids)
  ElMessage.success(`Kill 任务 #${res.jobId} 已提交`)
  await load()
}

onMounted(() => {
  void load()
})
</script>
