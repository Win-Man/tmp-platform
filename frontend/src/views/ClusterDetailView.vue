<template>
  <div class="page" v-loading="loading">
    <div class="detail-head">
      <div>
        <div class="title-row">
          <el-button text :icon="ArrowLeft" @click="router.push('/clusters')">返回</el-button>
          <h2 class="page-title">{{ cluster?.name || '集群详情' }}</h2>
          <StatusTag v-if="cluster" :status="cluster.status" />
          <el-tag v-if="cluster?.demo" type="danger" effect="dark" size="small">演示数据</el-tag>
          <el-tag v-if="cluster?.draft" type="info" effect="plain" size="small">草稿</el-tag>
          <el-tag v-if="cluster?.collecting" type="warning" effect="plain" size="small">采集中</el-tag>
        </div>
        <p class="page-desc">
          <span class="mono">中控机 {{ cluster ? `${cluster.tiupHost}:${cluster.tiupPort}` : '—' }}</span>
          · TiDB {{ cluster?.tidbVersion || '—' }}
          · {{ enums.label('deployMode', cluster?.deployMode || 'unknown') }}
          · 最近采集 {{ formatTime(cluster?.lastCollectAt) }}
        </p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" :loading="loading" @click="reload">刷新</el-button>
        <el-button
          v-if="auth.can('cluster.refresh')"
          type="primary"
          :loading="refreshing || polling"
          :disabled="cluster?.draft"
          @click="refreshCollect"
        >
          {{ polling ? '采集中…' : '立即采集' }}
        </el-button>
      </div>
    </div>

    <el-alert
      v-if="errorMessage"
      type="error"
      show-icon
      :closable="false"
      class="mb-12"
      :title="`集群信息加载失败：${errorMessage}`"
    >
      <el-button size="small" @click="reload">重试</el-button>
    </el-alert>

    <el-alert
      v-if="cluster?.demo"
      type="warning"
      show-icon
      :closable="false"
      class="mb-12"
      title="演示数据：该集群拓扑、实例、用户与巡检结果均为内置样例，不代表真实环境"
    />

    <el-alert
      v-if="cluster?.lastCollectError"
      type="error"
      show-icon
      :closable="false"
      class="mb-12"
      :title="`最近一次采集失败：${cluster.lastCollectError}`"
    />

    <el-card shadow="never">
      <!-- 每个 Tab 对应一条真实子路由（/clusters/:id/xxx），刷新与分享可直达 -->
      <el-tabs v-model="activeTab">
        <el-tab-pane v-for="tab in visibleTabs" :key="tab.name" :label="tab.label" :name="tab.name" />
      </el-tabs>

      <router-view v-slot="{ Component }">
        <!-- dataVersion 变化时重建当前 Tab，强制其重新拉取接口数据（刷新/采集完成） -->
        <component
          :is="Component"
          :key="`${clusterId}-${activeTab}-${dataVersion}`"
          :caps="activeTab === 'users' ? cluster?.capabilities : undefined"
          @change-tab="onChangeTab"
        />
      </router-view>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, Refresh } from '@element-plus/icons-vue'
import StatusTag from '@/components/StatusTag.vue'
import { clusterApi, type ClusterDetail } from '@/api'
import { formatTime } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'
import { useEnumStore } from '@/stores/enums'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const enums = useEnumStore()

const clusterId = computed(() => Number(route.params.id) || 0)
const cluster = ref<ClusterDetail | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const errorMessage = ref('')

/** 数据版本号：自增后当前 Tab 组件会重建，从而重新请求自身接口。 */
const dataVersion = ref(0)
/** 是否处于「等待采集任务完成」的轮询中。 */
const polling = ref(false)

const POLL_INTERVAL = 2000
const POLL_MAX_MS = 120000
let pollTimer: number | undefined
let pollElapsed = 0

/** Tab 定义：name 必须与子路由 path 段一致（/clusters/:id/<name>）。 */
const allTabs = [
  { name: 'overview', label: '概览', action: 'cluster.view' },
  { name: 'topology', label: '拓扑', action: 'cluster.view' },
  { name: 'instances', label: '实例', action: 'cluster.view' },
  { name: 'users', label: 'TiDB 用户', action: 'tidb_user.view' },
  { name: 'resource-groups', label: 'Resource Group', action: 'resource_group.view' },
  { name: 'sessions', label: '会话', action: 'session.view' },
  { name: 'params', label: '参数', action: 'variable.view' },
  { name: 'performance', label: '性能', action: 'cluster.view' },
  { name: 'inspection', label: '巡检', action: 'report.view' },
  { name: 'addr-maps', label: '地址映射', action: 'cluster.view' },
  { name: 'changes', label: '变更记录', action: 'cluster.view' },
] as const

const tabNames = allTabs.map((t) => t.name) as readonly string[]

const visibleTabs = computed(() => allTabs.filter((t) => auth.can(t.action)))

const activeTab = computed<string>({
  get: () => {
    const segment = route.path.split('/')[3] || ''
    return tabNames.includes(segment) ? segment : 'overview'
  },
  set: (name: string) => {
    if (!tabNames.includes(name)) {
      return
    }
    void router.push({ name: `cluster-${name}`, params: { id: clusterId.value } })
  },
})

async function loadCluster(silent = false): Promise<void> {
  if (!clusterId.value) {
    return
  }
  if (!silent) {
    loading.value = true
    errorMessage.value = ''
  }
  try {
    cluster.value = await clusterApi.get(clusterId.value)
  } catch (err) {
    // 静默轮询偶发失败不清空页面，避免一次抖动把详情冲掉。
    if (!silent) {
      // 失败时清空，避免用上一个集群的详情冒充当前集群。
      cluster.value = null
      errorMessage.value = err instanceof Error ? err.message : '请求失败'
    }
  } finally {
    if (!silent) {
      loading.value = false
    }
  }
}

/** 刷新：父级详情 + 当前 Tab 内容一起重取（Tab 靠 key 重建）。 */
async function reload(): Promise<void> {
  await loadCluster()
  dataVersion.value++
}

function stopPolling(): void {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
  polling.value = false
}

/** 采集任务异步执行，轮询集群详情直到 collecting 归零。 */
function startPolling(): void {
  if (pollTimer !== undefined) {
    return
  }
  polling.value = true
  pollElapsed = 0
  pollTimer = window.setInterval(() => {
    pollElapsed += POLL_INTERVAL
    if (pollElapsed > POLL_MAX_MS) {
      stopPolling()
      ElMessage.warning('采集耗时较长，已停止自动刷新，请稍后手动点「刷新」')
      return
    }
    void loadCluster(true)
  }, POLL_INTERVAL)
}

async function refreshCollect(): Promise<void> {
  refreshing.value = true
  try {
    const res = await clusterApi.refresh(clusterId.value)
    await loadCluster(true)
    if (cluster.value?.collecting) {
      startPolling()
      ElMessage.info(
        res.reused ? `已有进行中任务 #${res.jobId}，完成后自动刷新` : `采集任务 #${res.jobId} 已提交，完成后自动刷新`,
      )
    } else {
      // 任务极快结束时 collecting 已归零，直接刷新页面数据。
      dataVersion.value++
      ElMessage.success(`采集任务 #${res.jobId} 已完成`)
    }
  } finally {
    refreshing.value = false
  }
}

/** 子组件（概览页「查看实例」）切换 Tab 时同步路由。 */
function onChangeTab(name: string): void {
  activeTab.value = name
}

/** collecting 由 1 归 0 说明采集任务结束：自动刷新详情与当前 Tab。 */
watch(
  () => cluster.value?.collecting,
  (collecting) => {
    if (collecting) {
      startPolling()
      return
    }
    const wasPolling = pollTimer !== undefined
    stopPolling()
    if (!wasPolling) {
      return
    }
    dataVersion.value++
    if (cluster.value?.lastCollectError) {
      ElMessage.error(`采集失败：${cluster.value.lastCollectError}`)
    } else {
      ElMessage.success('采集完成，数据已更新')
    }
  },
)

watch(
  () => route.params.id,
  () => {
    stopPolling()
    void loadCluster()
  },
)

onMounted(async () => {
  await enums.ensure()
  await loadCluster()
})

onBeforeUnmount(() => {
  stopPolling()
})
</script>

<style scoped lang="scss">
.detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}

.title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;

  .page-title {
    margin: 0;
    font-size: 18px;
  }
}

.page-desc {
  margin: 6px 0 0;
  color: var(--tmp-text-muted);
  font-size: 13px;
}
</style>
