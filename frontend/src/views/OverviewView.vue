<template>
  <div class="page">
    <PageHeader title="总览" desc="集群健康、采集状态与近期任务的统一视图">
      <template #actions>
        <el-button :icon="Refresh" :loading="loading" @click="loadAll">刷新</el-button>
      </template>
    </PageHeader>

    <el-alert
      v-if="summary?.mockMode"
      type="warning"
      show-icon
      :closable="false"
      class="mb-12"
      title="当前为演示模式：数据来自内置 3 套假集群，不代表真实环境"
    />

    <el-row :gutter="16">
      <el-col :xs="12" :sm="12" :md="6" v-for="card in statCards" :key="card.label">
        <div class="stat-card">
          <div class="stat-label">{{ card.label }}</div>
          <div class="stat-value">{{ card.value }}</div>
          <div class="stat-extra">{{ card.extra }}</div>
        </div>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="mt-16">
      <el-col :xs="24" :md="10">
        <el-card shadow="never" header="集群健康分布">
          <div v-loading="loading">
            <EChart v-if="healthOption" :option="healthOption" height="260px" />
            <el-empty v-else description="暂无集群数据" :image-size="80" />
          </div>
        </el-card>
      </el-col>
      <el-col :xs="24" :md="14">
        <el-card shadow="never" header="TiDB 版本分布">
          <div v-loading="loading">
            <EChart v-if="versionOption" :option="versionOption" height="260px" />
            <el-empty v-else description="暂无版本数据" :image-size="80" />
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="mt-16">
      <el-col :xs="24" :md="10">
        <el-card shadow="never" header="最近一次巡检">
          <div v-loading="loading">
            <el-empty v-if="!latest?.found" description="尚未执行巡检" :image-size="70">
              <el-button v-if="auth.can('inspect.run')" type="primary" @click="router.push('/inspection')">
                去发起巡检
              </el-button>
            </el-empty>
            <div v-else class="inspection-block">
              <div class="inspection-head">
                <span class="cluster-name">{{ latest.clusterName }}</span>
                <StatusTag :status="latest.conclusion || ''" kind="conclusion" />
              </div>
              <div class="pass-rate">
                <span class="rate-value">{{ latest.passRate ?? 0 }}%</span>
                <span class="text-muted">通过率</span>
              </div>
              <div class="inspection-stats">
                <el-tag type="danger" effect="plain" size="small">严重 {{ latest.criticalCount ?? 0 }}</el-tag>
                <el-tag type="warning" effect="plain" size="small">警告 {{ latest.warningCount ?? 0 }}</el-tag>
                <span class="text-muted">{{ formatTime(latest.executedAt) }}</span>
              </div>
              <div class="mt-12">
                <el-button size="small" @click="router.push(`/inspection?run=${latest.runId}`)">查看运行</el-button>
                <el-button
                  v-if="latest.reportId"
                  size="small"
                  type="primary"
                  plain
                  @click="router.push(`/reports/${latest.reportId}`)"
                >
                  查看报告
                </el-button>
              </div>
            </div>
          </div>
        </el-card>

        <el-card shadow="never" header="采集状态" class="mt-16">
          <div v-loading="loading" class="collect-block">
            <div class="detail-item"><span class="detail-label">最近采集</span><span class="detail-value">{{ formatTime(collectStatus?.lastCollectAt) }}</span></div>
            <div class="detail-item"><span class="detail-label">成功集群</span><span class="detail-value">{{ collectStatus?.successCount ?? 0 }}</span></div>
            <div class="detail-item"><span class="detail-label">失败集群</span><span class="detail-value">{{ collectStatus?.failedCount ?? 0 }}</span></div>
            <div class="detail-item"><span class="detail-label">数据源</span><span class="detail-value">{{ collectStatus?.dataSource || '—' }}</span></div>
          </div>
        </el-card>
      </el-col>

      <el-col :xs="24" :md="14">
        <el-card shadow="never" header="近期任务">
          <el-table v-loading="loading" :data="recentJobs" size="small" empty-text="暂无任务">
            <el-table-column prop="id" label="ID" width="70" />
            <el-table-column label="类型" width="130">
              <template #default="{ row }">{{ row.typeLabel || row.type }}</template>
            </el-table-column>
            <el-table-column prop="targetLabel" label="对象" min-width="130" show-overflow-tooltip />
            <el-table-column label="状态" width="110">
              <template #default="{ row }"><StatusTag :status="row.status" kind="job" /></template>
            </el-table-column>
            <el-table-column label="进度" width="110">
              <template #default="{ row }">
                <el-progress :percentage="row.progress" :stroke-width="6" :show-text="false" />
              </template>
            </el-table-column>
            <el-table-column label="创建时间" width="160">
              <template #default="{ row }">{{ formatTime(row.createdAt, 'MM-DD HH:mm:ss') }}</template>
            </el-table-column>
            <el-table-column label="操作" width="80" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" @click="router.push(`/jobs/${row.id}`)">详情</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import type { EChartsOption } from 'echarts'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import EChart from '@/components/EChart.vue'
import { overviewApi } from '@/api'
import type { CollectStatusInfo, HealthItem, JobRow, LatestInspection, OverviewSummary } from '@/api/types'
import { formatTime } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

const loading = ref(false)
const summary = ref<OverviewSummary | null>(null)
const health = ref<HealthItem[]>([])
const latest = ref<LatestInspection | null>(null)
const collectStatus = ref<CollectStatusInfo | null>(null)
const recentJobs = ref<JobRow[]>([])

const statCards = computed(() => [
  {
    label: '纳管集群',
    value: summary.value?.clusterTotal ?? 0,
    extra: `草稿不计入统计`,
  },
  {
    label: '实例总数',
    value: summary.value?.instanceTotal ?? 0,
    extra: '来自三通道采集快照',
  },
  {
    label: '主机总数',
    value: summary.value?.hostTotal ?? 0,
    extra: '去重后的物理机 / 虚拟机',
  },
  {
    label: '异常集群',
    value: summary.value?.abnormalTotal ?? 0,
    extra: `状态异常或存在非 Up 实例 · 异常实例 ${summary.value?.instanceAbnormalTotal ?? 0} 个`,
  },
])

const healthOption = computed<EChartsOption | null>(() => {
  const total = health.value.reduce((sum, item) => sum + item.count, 0)
  if (total === 0) {
    return null
  }
  const labelMap: Record<string, string> = { healthy: '正常', degraded: '降级', error: '异常', unknown: '未知' }
  const colorMap: Record<string, string> = {
    healthy: '#67c23a',
    degraded: '#e6a23c',
    error: '#f56c6c',
    unknown: '#909399',
  }
  return {
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, icon: 'circle' },
    series: [
      {
        type: 'pie',
        radius: ['46%', '68%'],
        avoidLabelOverlap: true,
        label: { formatter: '{b}\n{c}' },
        data: health.value.map((item) => ({
          name: labelMap[item.status] || item.status,
          value: item.count,
          itemStyle: { color: colorMap[item.status] || '#909399' },
        })),
      },
    ],
  }
})

const versionOption = computed<EChartsOption | null>(() => {
  const dist = summary.value?.tidbVersionDist || {}
  const versions = Object.keys(dist).sort()
  if (versions.length === 0) {
    return null
  }
  return {
    grid: { left: 40, right: 24, top: 24, bottom: 40 },
    tooltip: { trigger: 'axis' },
    xAxis: { type: 'category', data: versions, axisTick: { show: false } },
    yAxis: { type: 'value', minInterval: 1 },
    series: [
      {
        type: 'bar',
        barWidth: 42,
        data: versions.map((v) => dist[v]),
        itemStyle: { color: '#2f6fed', borderRadius: [4, 4, 0, 0] },
      },
    ],
  }
})

async function loadAll(): Promise<void> {
  loading.value = true
  try {
    const [summaryRes, healthRes, latestRes, collectRes, jobsRes] = await Promise.all([
      overviewApi.summary(),
      overviewApi.health(),
      overviewApi.latestInspection(),
      overviewApi.collectStatus(),
      overviewApi.recentJobs(10),
    ])
    summary.value = summaryRes
    health.value = healthRes
    latest.value = latestRes
    collectStatus.value = collectRes
    recentJobs.value = jobsRes || []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void loadAll()
})
</script>

<style scoped lang="scss">
.inspection-block {
  .inspection-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  .cluster-name {
    font-weight: 600;
  }

  .pass-rate {
    display: flex;
    align-items: baseline;
    gap: 8px;
    margin: 8px 0;
  }

  .rate-value {
    font-size: 28px;
    font-weight: 600;
    color: #2f6fed;
  }

  .inspection-stats {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
}

.collect-block {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
</style>
