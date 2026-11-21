<template>
  <div v-loading="loading">
    <el-row :gutter="16">
      <el-col :xs="24" :md="14">
        <h4 class="section-title">纳管信息</h4>
        <div class="detail-grid">
          <div class="detail-item"><span class="detail-label">TiUP 主机</span><span class="detail-value mono">{{ cluster?.tiupHost }}:{{ cluster?.tiupPort }}</span></div>
          <div class="detail-item"><span class="detail-label">TiUP 用户</span><span class="detail-value">{{ cluster?.tiupUser }}</span></div>
          <div class="detail-item"><span class="detail-label">TiUP 目录</span><span class="detail-value mono">{{ cluster?.tiupDir || '—' }}</span></div>
          <div class="detail-item"><span class="detail-label">DB 地址</span><span class="detail-value mono">{{ cluster?.dbHost }}:{{ cluster?.dbPort }}</span></div>
          <div class="detail-item"><span class="detail-label">DB 用户</span><span class="detail-value">{{ cluster?.dbUser }}</span></div>
          <div class="detail-item"><span class="detail-label">PD 地址</span><span class="detail-value mono">{{ cluster?.pdEndpoint || '—' }}</span></div>
          <div class="detail-item"><span class="detail-label">CPU 架构</span><span class="detail-value">{{ cluster?.cpuArch }}</span></div>
          <div class="detail-item"><span class="detail-label">部署模式</span><span class="detail-value">{{ enums.label('deployMode', cluster?.deployMode || 'unknown') }}</span></div>
        </div>

        <h4 class="section-title mt-16">访问入口</h4>
        <div class="detail-grid">
          <div class="detail-item">
            <span class="detail-label">Dashboard</span>
            <span class="detail-value">
              <template v-if="cluster?.dashboardUrls?.length">
                <a v-for="url in cluster.dashboardUrls" :key="url" :href="url" target="_blank" class="link">{{ url }}</a>
              </template>
              <span v-else class="text-muted">—</span>
            </span>
          </div>
          <div class="detail-item"><span class="detail-label">Grafana</span><span class="detail-value mono">{{ (cluster?.grafanaUrls || []).join(', ') || '—' }}</span></div>
          <div class="detail-item"><span class="detail-label">Prometheus</span><span class="detail-value mono">{{ (cluster?.prometheusUrls || []).join(', ') || '—' }}</span></div>
          <div class="detail-item"><span class="detail-label">Alertmanager</span><span class="detail-value mono">{{ (cluster?.alertmanagerUrls || []).join(', ') || '—' }}</span></div>
        </div>
      </el-col>

      <el-col :xs="24" :md="10">
        <h4 class="section-title">能力矩阵</h4>
        <div class="caps">
          <div class="cap-item">
            <span>Resource Group</span>
            <el-tag :type="caps?.supportsResourceGroup ? 'success' : 'info'" effect="plain" size="small">
              {{ caps?.supportsResourceGroup ? '支持（v7.5+）' : '不支持' }}
            </el-tag>
          </div>
          <div class="cap-item">
            <span>Kill 会话</span>
            <el-tag :type="caps?.supportsSessionKill ? 'success' : 'info'" effect="plain" size="small">
              {{ caps?.supportsSessionKill ? '支持' : '不支持' }}
            </el-tag>
          </div>
          <div class="cap-item">
            <span>Placement Rules</span>
            <el-tag :type="caps?.supportsPlacementRules ? 'success' : 'info'" effect="plain" size="small">
              {{ caps?.supportsPlacementRules ? '支持（v5.0+）' : '不支持' }}
            </el-tag>
          </div>
          <div class="cap-item">
            <span>PD 状态</span>
            <el-tag :type="overview?.pdUnavailable ? 'warning' : 'success'" effect="plain" size="small">
              {{ overview?.pdUnavailable ? '不可用（显示 —）' : '正常' }}
            </el-tag>
          </div>
        </div>

        <h4 class="section-title mt-16">采集快照</h4>
        <div class="detail-grid">
          <div class="detail-item"><span class="detail-label">快照时间</span><span class="detail-value">{{ formatTime(overview?.fetchedAt) }}</span></div>
          <div class="detail-item"><span class="detail-label">版本一致</span><span class="detail-value">{{ overview?.versionConsistent ? '是' : '否' }}</span></div>
          <div class="detail-item"><span class="detail-label">版本集合</span><span class="detail-value mono">{{ (overview?.versions || []).join(', ') || '—' }}</span></div>
          <div class="detail-item"><span class="detail-label">采集错误</span><span class="detail-value">{{ overview?.collectError || '无' }}</span></div>
        </div>
      </el-col>
    </el-row>

    <h4 class="section-title mt-16">
      组件聚合
      <el-button link type="primary" class="ml" @click="emit('change-tab', 'instances')">查看实例</el-button>
    </h4>
    <el-table :data="overview?.components || []" size="small" empty-text="暂无组件数据">
      <el-table-column prop="component" label="组件" width="140" />
      <el-table-column prop="instanceCount" label="实例数" width="90" />
      <el-table-column label="版本" min-width="160">
        <template #default="{ row }">{{ (row.versions || []).join(', ') || '—' }}</template>
      </el-table-column>
      <el-table-column label="运行状态" width="180">
        <template #default="{ row }">
          <span class="status-dot dot-success" />Up {{ row.upCount }}
          <span class="status-dot dot-danger" style="margin-left: 12px" />Down {{ row.downCount }}
        </template>
      </el-table-column>
      <el-table-column label="健康" width="110">
        <template #default="{ row }">
          <StatusTag :status="row.downCount > 0 ? 'degraded' : 'healthy'" />
        </template>
      </el-table-column>
    </el-table>

    <h4 class="section-title mt-16">最近任务</h4>
    <el-table :data="overview?.recentJobs || []" size="small" empty-text="暂无任务">
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column label="类型" width="140">
        <template #default="{ row }">{{ row.typeLabel || row.type }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }"><StatusTag :status="row.status" kind="job" /></template>
      </el-table-column>
      <el-table-column prop="currentStep" label="当前步骤" min-width="140" />
      <el-table-column label="创建时间" width="160">
        <template #default="{ row }">{{ formatTime(row.createdAt, 'MM-DD HH:mm:ss') }}</template>
      </el-table-column>
      <el-table-column label="操作" width="80">
        <template #default="{ row }">
          <router-link :to="`/jobs/${row.id}`" class="link">详情</router-link>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import StatusTag from '@/components/StatusTag.vue'
import { clusterApi, type ClusterDetail, type ClusterOverview } from '@/api'
import type { Capabilities, JobRow } from '@/api/types'
import { formatTime } from '@/utils/format'
import { useEnumStore } from '@/stores/enums'

const props = defineProps<{ clusterId: number }>()
const emit = defineEmits<{ 'change-tab': [string] }>()

const enums = useEnumStore()
const loading = ref(false)
const overview = ref<ClusterOverview | null>(null)

const cluster = computed<ClusterDetail | null>(() => overview.value?.cluster || null)
const caps = computed<Capabilities | undefined>(() => cluster.value?.capabilities)
const recentJobs = computed<JobRow[]>(() => overview.value?.recentJobs || [])

async function load(): Promise<void> {
  loading.value = true
  try {
    overview.value = await clusterApi.overview(props.clusterId)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})
</script>

<style scoped lang="scss">
.section-title {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 12px;
}

.caps {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.cap-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
  border-bottom: 1px dashed var(--tmp-border);
  padding-bottom: 8px;
}

.link {
  color: #2f6fed;
  text-decoration: none;
  display: block;
}

.ml {
  margin-left: 8px;
}
</style>
