<template>
  <div class="page">
    <PageHeader title="主机管理" desc="纳管集群的物理机 / 虚拟机资源使用情况与 SSH 连通性">
      <template #actions>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button
          v-if="auth.can('host.ssh.check')"
          type="primary"
          :disabled="selectedIds.length === 0"
          @click="runSSHCheck"
        >
          SSH 检测（{{ selectedIds.length }}）
        </el-button>
      </template>
    </PageHeader>

    <el-card shadow="never">
      <div class="table-toolbar">
        <div class="toolbar-left">
          <el-radio-group v-model="viewMode" size="small" @change="syncUrl">
            <el-radio-button value="flat">列表视图</el-radio-button>
            <el-radio-button value="grouped">按集群分组</el-radio-button>
          </el-radio-group>
          <el-input
            v-model="filters.keyword"
            placeholder="IP / 主机名"
            clearable
            style="width: 200px"
            :prefix-icon="Search"
            @keyup.enter="search"
            @clear="search"
          />
          <el-select v-model="filters.arch" placeholder="CPU 架构" clearable style="width: 140px" @change="search">
            <el-option v-for="arch in arches" :key="arch" :label="arch" :value="arch" />
          </el-select>
          <el-select
            v-model="filters.clusterId"
            placeholder="所属集群"
            clearable
            filterable
            style="width: 220px"
            @change="search"
          >
            <!-- 不同中控机上允许同名集群，下拉项带中控机才能区分 -->
            <el-option
              v-for="c in clusters"
              :key="c.id"
              :label="clusterOptionLabel(c)"
              :value="c.id"
            />
          </el-select>
          <el-button @click="search">查询</el-button>
        </div>
        <div class="toolbar-right text-muted">共 {{ total }} 台主机</div>
      </div>

      <template v-if="viewMode === 'flat'">
        <el-alert
          v-if="errorMessage"
          type="error"
          show-icon
          :closable="false"
          class="mb-12"
          :title="`主机列表加载失败：${errorMessage}`"
        >
          <el-button size="small" @click="load">重试</el-button>
        </el-alert>

        <el-table
          v-loading="loading"
          :data="rows"
          size="small"
          empty-text="暂无主机数据，请先完成集群采集"
          @selection-change="onSelectionChange"
        >
          <el-table-column type="selection" width="44" />
          <el-table-column label="主机" min-width="210">
            <template #default="{ row }">
              <div class="mono">
                {{ row.ip }}
                <el-tooltip
                  v-if="row.mappedIp"
                  :content="`访问时实际连接 ${row.mappedIp}（地址映射规则生效，SSH 检测也走该地址）`"
                  placement="top"
                >
                  <el-tag size="small" type="success" effect="plain" style="margin-left: 6px">
                    → {{ row.mappedIp }}
                  </el-tag>
                </el-tooltip>
              </div>
              <div class="text-muted" style="font-size: 12px">{{ row.hostname || '—' }}</div>
            </template>
          </el-table-column>
          <el-table-column label="所属集群" min-width="200">
            <template #default="{ row }">
              <template v-if="hostClusters(row).length > 0">
                <el-tooltip
                  v-for="c in hostClusters(row)"
                  :key="c.id"
                  :content="`中控机 ${c.tiupHost || '—'}`"
                  placement="top"
                >
                  <router-link
                    :to="`/clusters/${c.id}`"
                    class="cluster-tag no-underline"
                  >
                    <el-tag size="small" effect="plain" :type="duplicateName(row, c.name) ? 'warning' : 'primary'">
                      {{ clusterTagText(row, c) }}
                    </el-tag>
                  </router-link>
                </el-tooltip>
              </template>
              <span v-else class="text-muted">—</span>
            </template>
          </el-table-column>
          <el-table-column label="角色" min-width="200">
            <template #default="{ row }">
              <el-tag v-for="role in row.roles || []" :key="role" size="small" effect="plain" style="margin-right: 4px">
                {{ role }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="架构" width="110">
            <template #default="{ row }">
              <span v-if="row.cpuArch" class="mono">{{ row.cpuArch }}</span>
              <span v-else class="text-muted">—</span>
            </template>
          </el-table-column>
          <el-table-column label="规格" width="140">
            <template #default="{ row }">
              <span v-if="hasSpec(row)">{{ row.cpuCores }}C / {{ fmtNum(row.memoryGb, 1) }}G</span>
              <span v-else class="text-muted">—</span>
            </template>
          </el-table-column>
          <el-table-column label="CPU" width="120">
            <template #default="{ row }">
              <el-progress :percentage="Math.round(row.cpuUsagePct)" :color="usageColor(row.cpuUsagePct)" :stroke-width="8" />
            </template>
          </el-table-column>
          <el-table-column label="内存" width="120">
            <template #default="{ row }">
              <el-progress :percentage="Math.round(row.memUsagePct)" :color="usageColor(row.memUsagePct)" :stroke-width="8" />
            </template>
          </el-table-column>
          <el-table-column label="磁盘" width="120">
            <template #default="{ row }">
              <el-progress :percentage="Math.round(row.diskUsagePct)" :color="usageColor(row.diskUsagePct)" :stroke-width="8" />
            </template>
          </el-table-column>
          <el-table-column label="SSH" width="100">
            <template #default="{ row }"><StatusTag :status="row.sshStatus" kind="ssh" /></template>
          </el-table-column>
          <el-table-column label="资源采集时间" width="160">
            <template #default="{ row }">{{ formatTime(row.resourceFetchedAt, 'MM-DD HH:mm:ss') }}</template>
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
      </template>

      <template v-else>
        <div v-loading="loading">
          <el-empty v-if="groups.length === 0" :description="emptyText" />
          <div v-for="group in groups" :key="group.clusterId" class="host-group">
            <div class="group-head">
              <span class="group-name">{{ group.clusterName }}</span>
              <span class="text-muted">{{ group.hosts.length }} 台主机</span>
            </div>
            <el-table :data="group.hosts" size="small">
              <el-table-column label="主机" min-width="210">
                <template #default="{ row }">
                  <span class="mono">{{ row.ip }}</span>
                  <el-tooltip
                    v-if="row.mappedIp"
                    :content="`访问时实际连接 ${row.mappedIp}（地址映射规则生效）`"
                    placement="top"
                  >
                    <el-tag size="small" type="success" effect="plain" style="margin-left: 6px">
                      → {{ row.mappedIp }}
                    </el-tag>
                  </el-tooltip>
                  <span class="text-muted" style="margin-left: 8px">{{ row.hostname }}</span>
                </template>
              </el-table-column>
              <el-table-column label="角色" min-width="200">
                <template #default="{ row }">
                  <el-tag v-for="role in row.roles || []" :key="role" size="small" effect="plain" style="margin-right: 4px">
                    {{ role }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="规格" width="130">
                <template #default="{ row }">
                  <span v-if="hasSpec(row)">{{ row.cpuCores }}C / {{ fmtNum(row.memoryGb, 1) }}G</span>
                  <span v-else class="text-muted">—</span>
                </template>
              </el-table-column>
              <el-table-column label="CPU / 内存 / 磁盘" width="220">
                <template #default="{ row }">
                  <span class="text-muted">
                    {{ fmtNum(row.cpuUsagePct) }}% / {{ fmtNum(row.memUsagePct) }}% / {{ fmtNum(row.diskUsagePct) }}%
                  </span>
                </template>
              </el-table-column>
              <el-table-column label="SSH" width="100">
                <template #default="{ row }"><StatusTag :status="row.sshStatus" kind="ssh" /></template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </template>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'
import { Refresh, Search } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import { clusterApi, hostApi } from '@/api'
import type { ClusterOption, HostClusterBrief, HostRow } from '@/api/types'
import { fmtNum, formatTime, usageColor } from '@/utils/format'
import { queryInt, queryString, syncQuery } from '@/utils/query'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const loading = ref(false)
const viewMode = ref<'flat' | 'grouped'>('flat')
const rows = ref<HostRow[]>([])
const total = ref(0)
const arches = ref<string[]>([])
const clusters = ref<ClusterOption[]>([])
const selectedIds = ref<number[]>([])
const groups = ref<{ clusterId: number; clusterName: string; hosts: HostRow[] }[]>([])
const errorMessage = ref('')

const filters = reactive({ keyword: '', arch: '', clusterId: undefined as number | undefined, page: 1, pageSize: 20 })

/** 分组视图下是否有任一搜索/筛选条件生效（该视图没有 sshStatus 控件，故不纳入）。 */
const hasActiveFilter = computed(
  () => filters.keyword.trim() !== '' || filters.arch !== '' || filters.clusterId !== undefined,
)

/** 分组空态文案：无筛选 → 原文案；有筛选且无匹配 → 空白（对齐集群列表口径，用户明确不提示）。 */
const emptyText = computed(() => (hasActiveFilter.value ? '' : '暂无分组数据'))

/** 把筛选与分页写入 URL query。 */
function syncUrl(): void {
  syncQuery(router, route.query, {
    view: viewMode.value === 'grouped' ? 'grouped' : undefined,
    keyword: filters.keyword,
    arch: filters.arch,
    clusterId: filters.clusterId,
    page: filters.page > 1 ? filters.page : undefined,
    pageSize: filters.pageSize !== 20 ? filters.pageSize : undefined,
  })
}

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const params = {
      keyword: filters.keyword || undefined,
      arch: filters.arch || undefined,
      clusterId: filters.clusterId || undefined,
    }
    const res = await hostApi.list({
      page: filters.page,
      pageSize: filters.pageSize,
      ...params,
    })
    rows.value = res.list || []
    total.value = res.total || 0
    arches.value = res.arches || []
    const grouped = await hostApi.grouped(params)
    groups.value = grouped.groups || []
  } catch (err) {
    rows.value = []
    total.value = 0
    groups.value = []
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

function onSelectionChange(val: HostRow[]): void {
  selectedIds.value = val.map((h) => h.id)
}

/** 主机所属集群明细；后端未返回时退回 clusterNames 构造（兼容旧接口）。 */
function hostClusters(row: HostRow): HostClusterBrief[] {
  if (row.clusters && row.clusters.length > 0) {
    return row.clusters
  }
  if (!row.clusterNames || row.clusterNames.length === 0) {
    return []
  }
  const ids = row.clusterIds || []
  return row.clusterNames.map((name, i) => ({ id: ids[i] || 0, name, tiupHost: '' }))
}

/** 同一台主机上是否出现了同名集群（多套集群可能重名，需靠中控机区分）。 */
function duplicateName(row: HostRow, name: string): boolean {
  return hostClusters(row).filter((c) => c.name === name).length > 1
}

/** 集群标签文本：同名集群追加 @中控机，否则只显示集群名（中控机在 hover 提示里）。 */
function clusterTagText(row: HostRow, c: HostClusterBrief): string {
  if (duplicateName(row, c.name) && c.tiupHost) {
    return `${c.name}@${c.tiupHost}`
  }
  return c.name
}

/** 所属集群筛选下拉项文案：集群名 @ 中控机（中控机为空时不追加）。 */
function clusterOptionLabel(c: ClusterOption): string {
  return c.tiupHost ? `${c.name} @ ${c.tiupHost}` : c.name
}

/** 是否有硬件规格（0 值说明还没采到，展示 — 而不是 0C / 0G）。 */
function hasSpec(row: HostRow): boolean {
  return (row.cpuCores || 0) > 0 || (row.memoryGb || 0) > 0
}

async function runSSHCheck(): Promise<void> {
  const res = await hostApi.sshCheck(selectedIds.value)
  ElMessage.success(`SSH 检测任务 #${res.jobId} 已提交`)
  await router.push(`/jobs/${res.jobId}`)
}

onMounted(async () => {
  // 从 URL 恢复视图与筛选（刷新 / 分享链接）。
  viewMode.value = queryString(route.query, 'view') === 'grouped' ? 'grouped' : 'flat'
  filters.keyword = queryString(route.query, 'keyword')
  filters.arch = queryString(route.query, 'arch')
  const clusterIdParam = queryInt(route.query, 'clusterId', 0)
  filters.clusterId = clusterIdParam > 0 ? clusterIdParam : undefined
  filters.page = queryInt(route.query, 'page', 1)
  filters.pageSize = queryInt(route.query, 'pageSize', 20)
  clusters.value = await clusterApi.options().catch(() => [])
  await load()
})
</script>

<style scoped lang="scss">
.host-group {
  margin-bottom: 16px;
}

.group-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.group-name {
  font-weight: 600;
}

/* 所属集群标签：多集群共享主机时一行展示多个 */
.cluster-tag {
  display: inline-block;
  margin: 0 4px 2px 0;
}
</style>
