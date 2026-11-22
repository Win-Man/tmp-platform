<template>
  <div>
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-input
          v-model="keyword"
          placeholder="用户 / 实例 / 库 / SQL 关键词"
          clearable
          style="width: 240px"
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
        <el-tag v-if="live" size="small" type="success" effect="plain">实时</el-tag>
        <el-tag v-else size="small" type="info" effect="plain">采集快照</el-tag>
        <span class="text-muted session-meta">
          共 {{ total }} 个会话<template v-if="fetchedAt"> · {{ formatTime(fetchedAt, 'HH:mm:ss') }}</template>
        </span>
        <el-button :icon="Refresh" :loading="loading" @click="refresh">刷新会话</el-button>
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

    <p class="text-muted session-hint">
      实时读取 <span class="mono">information_schema.cluster_processlist</span>，覆盖集群全部 TiDB 实例；
      查询失败时回退到上次采集的快照。
    </p>

    <el-table
      v-loading="loading"
      :data="rows"
      size="small"
      empty-text="暂无会话数据"
      @selection-change="onSelectionChange"
    >
      <el-table-column type="selection" width="44" />
      <el-table-column label="实例" width="160">
        <template #default="{ row }"><span class="mono">{{ row.instance || '—' }}</span></template>
      </el-table-column>
      <el-table-column prop="sessionId" label="会话 ID" width="96" />
      <el-table-column prop="userName" label="用户" width="120" />
      <el-table-column label="来源" width="150">
        <template #default="{ row }"><span class="mono">{{ row.host }}</span></template>
      </el-table-column>
      <el-table-column label="数据库" width="110">
        <template #default="{ row }">{{ row.db || '—' }}</template>
      </el-table-column>
      <el-table-column prop="command" label="命令" width="90" />
      <el-table-column label="执行时长" width="100">
        <template #default="{ row }">
          <el-tag v-if="row.timeSec >= 60" size="small" type="warning" effect="plain">{{ row.timeSec }}s</el-tag>
          <span v-else>{{ row.timeSec }}s</span>
        </template>
      </el-table-column>
      <el-table-column prop="state" label="状态" width="120" />
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
import { Refresh, Search } from '@element-plus/icons-vue'
import { tidbApi } from '@/api'
import type { SessionRow, SessionsPage } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { formatTime } from '@/utils/format'

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
/** live=true 表示当前展示的是实时查询结果，false 为采集快照。 */
const live = ref(false)
const fetchedAt = ref('')

function apply(res: SessionsPage, isLive: boolean): void {
  rows.value = res.list || []
  total.value = res.total || 0
  live.value = isLive && res.live !== false
  fetchedAt.value = res.fetchedAt || res.list?.[0]?.fetchedAt || ''
}

function params(): Record<string, unknown> {
  return {
    page: page.value,
    pageSize: pageSize.value,
    keyword: keyword.value || undefined,
    sortBy: sortBy.value,
  }
}

/** 拉取快照（采集结果）。 */
async function loadSnapshot(): Promise<void> {
  apply(await tidbApi.sessions(props.clusterId, params()), false)
}

/**
 * 刷新会话：优先实时查 cluster_processlist，
 * 失败（无凭据 / 网络不通 / 权限不足）则回退快照并提示原因。
 */
async function loadLive(): Promise<void> {
  loading.value = true
  try {
    try {
      const res = await tidbApi.refreshSessions(props.clusterId, params(), true)
      apply(res, true)
      return
    } catch (err) {
      const reason = err instanceof Error ? err.message : '未知错误'
      ElMessage.warning(`实时查询失败，已展示上次采集快照：${reason}`)
    }
    await loadSnapshot()
  } finally {
    loading.value = false
  }
}

async function refresh(): Promise<void> {
  await loadLive()
  if (live.value) {
    ElMessage.success('已获取最新会话')
  }
}

function search(): void {
  page.value = 1
  void loadLive()
}

function onPageChange(p: number): void {
  page.value = p
  void loadLive()
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
  await loadLive()
}

onMounted(() => {
  void loadLive()
})
</script>

<style scoped>
.session-meta {
  margin-right: 8px;
}

.session-hint {
  margin: -4px 0 8px;
  font-size: 12px;
  line-height: 18px;
}
</style>
