<template>
  <div class="page" v-loading="loading">
    <div class="detail-head">
      <div>
        <div class="title-row">
          <el-button text :icon="ArrowLeft" @click="router.push('/clusters')">返回</el-button>
          <h2 class="page-title">{{ cluster?.name || '集群详情' }}</h2>
          <StatusTag v-if="cluster" :status="cluster.status" />
          <el-tag v-if="cluster?.draft" type="info" effect="plain" size="small">草稿</el-tag>
          <el-tag v-if="cluster?.collecting" type="warning" effect="plain" size="small">采集中</el-tag>
        </div>
        <p class="page-desc">
          <span class="mono">{{ cluster?.tiupHost }}:{{ cluster?.tiupPort }}</span>
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
          :loading="refreshing"
          :disabled="cluster?.draft"
          @click="refreshCollect"
        >
          立即采集
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
        <component
          :is="Component"
          :caps="activeTab === 'users' ? cluster?.capabilities : undefined"
          @change-tab="onChangeTab"
        />
      </router-view>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
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

async function loadCluster(): Promise<void> {
  if (!clusterId.value) {
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    cluster.value = await clusterApi.get(clusterId.value)
  } catch (err) {
    // 失败时清空，避免用上一个集群的详情冒充当前集群。
    cluster.value = null
    errorMessage.value = err instanceof Error ? err.message : '请求失败'
  } finally {
    loading.value = false
  }
}

async function reload(): Promise<void> {
  await loadCluster()
}

async function refreshCollect(): Promise<void> {
  refreshing.value = true
  try {
    const res = await clusterApi.refresh(clusterId.value)
    ElMessage.success(res.reused ? `已有进行中任务 #${res.jobId}` : `采集任务 #${res.jobId} 已提交`)
    await loadCluster()
  } finally {
    refreshing.value = false
  }
}

/** 子组件（概览页「查看实例」）切换 Tab 时同步路由。 */
function onChangeTab(name: string): void {
  activeTab.value = name
}

watch(
  () => route.params.id,
  () => {
    void loadCluster()
  },
)

onMounted(async () => {
  await enums.ensure()
  await loadCluster()
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
