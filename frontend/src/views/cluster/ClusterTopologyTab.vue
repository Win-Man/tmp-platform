<template>
  <div v-loading="loading">
    <div class="table-toolbar">
      <div class="toolbar-left text-muted">
        共 {{ hostCount }} 台主机 / {{ instanceCount }} 个实例 · {{ groups.length }} 类组件（数据来自 SSH + MySQL + PD 三通道归一化结果）
      </div>
      <div class="toolbar-right">
        <el-radio-group v-model="view" size="small">
          <el-radio-button value="host">按主机</el-radio-button>
          <el-radio-button value="component">按组件</el-radio-button>
        </el-radio-group>
        <el-button size="small" :icon="Refresh" @click="load">刷新</el-button>
      </div>
    </div>

    <el-empty v-if="!loading && !hosts.length && !groups.length" description="暂无拓扑数据，请先执行采集" />

    <!-- 主视角：每台主机上部署了哪些实例 -->
    <div v-if="view === 'host'" class="topology">
      <div v-for="host in hosts" :key="host.hostIp" class="topo-host">
        <div class="host-head">
          <div class="host-id">
            <el-icon class="host-icon"><Monitor /></el-icon>
            <span class="host-name">{{ host.hostname || host.hostIp }}</span>
            <span v-if="host.hostname" class="text-muted mono host-ip">{{ host.hostIp }}</span>
          </div>
          <div class="host-tags">
            <el-tag size="small" effect="plain">{{ host.instanceCount }} 实例</el-tag>
            <el-tag
              v-if="host.sshStatus === 'ok' || host.sshStatus === 'fail'"
              size="small"
              effect="plain"
              :type="sshTagType(host.sshStatus)"
            >
              {{ sshLabel(host.sshStatus) }}
            </el-tag>
          </div>
        </div>

        <div v-if="host.cpuCores || host.memoryGb" class="host-spec text-muted">
          <span v-if="host.cpuCores">{{ host.cpuCores }}C</span>
          <span v-if="host.memoryGb">{{ formatMem(host.memoryGb) }}</span>
        </div>

        <div class="inst-list">
          <div v-for="inst in host.instances" :key="inst.id" class="inst-row">
            <el-tag size="small" effect="plain" :type="compTagType(inst.component)" class="inst-comp">
              {{ inst.component.toUpperCase() }}
            </el-tag>
            <span class="status-dot" :class="statusDot(inst.status)" />
            <span class="mono inst-addr">{{ inst.port }}</span>
            <span
              v-if="inst.statusPort && inst.statusPort !== inst.port"
              class="text-muted port-extra"
              title="实例端口 / 状态端口"
            >
              / {{ inst.statusPort }}
            </span>
            <el-tag v-if="inst.role === 'leader'" size="small" effect="dark" type="warning" class="ml6">leader</el-tag>
            <span class="text-muted inst-ver">{{ inst.version || '—' }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 备选视角：按组件分组（保留原有展示） -->
    <div v-else class="topology">
      <div v-for="group in groups" :key="group.component" class="topo-group">
        <div class="topo-head">
          <span class="topo-component">{{ group.component.toUpperCase() }}</span>
          <el-tag size="small" effect="plain">{{ group.nodes.length }} 实例</el-tag>
        </div>
        <div class="topo-nodes">
          <div v-for="node in group.nodes" :key="node.id" class="topo-node">
            <div class="node-line">
              <span class="status-dot" :class="statusDot(node.status)" />
              <span class="mono">{{ node.hostIp }}:{{ node.port }}</span>
            </div>
            <div class="node-meta">
              <el-tag size="small" effect="plain" :type="node.role === 'leader' ? 'warning' : 'info'">
                {{ node.role }}
              </el-tag>
              <span class="text-muted">{{ node.version }}</span>
            </div>
            <div v-if="node.az" class="text-muted node-az">AZ: {{ node.az }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Monitor, Refresh } from '@element-plus/icons-vue'
import { clusterApi } from '@/api'
import type { TopologyGroup, TopologyHost } from '@/api/types'

const props = defineProps<{ clusterId: number }>()

type ViewMode = 'host' | 'component'
type TagType = 'primary' | 'success' | 'warning' | 'info' | 'danger'

const loading = ref(false)
const view = ref<ViewMode>('host')
const hosts = ref<TopologyHost[]>([])
const groups = ref<TopologyGroup[]>([])
const nodeCount = ref(0)

const hostCount = computed(() => hosts.value.length)
const instanceCount = computed(() =>
  nodeCount.value || hosts.value.reduce((s, h) => s + (h.instances?.length || 0), 0),
)

const COMP_TAG: Record<string, TagType> = {
  pd: 'warning',
  tidb: 'primary',
  tikv: 'success',
  tiflash: 'success',
  ticdc: 'info',
}

function compTagType(comp: string): TagType {
  return COMP_TAG[comp] || 'info'
}

function statusDot(status: string): string {
  if (status === 'up') return 'dot-success'
  if (status === 'down' || status === 'unhealthy') return 'dot-danger'
  return 'dot-info'
}

function sshLabel(status: string): string {
  if (status === 'ok' || status === 'success') return 'SSH 通'
  if (status === 'fail' || status === 'failed') return 'SSH 不通'
  return 'SSH 未检测'
}

function sshTagType(status: string): 'success' | 'danger' | 'info' {
  if (status === 'ok' || status === 'success') return 'success'
  if (status === 'fail' || status === 'failed') return 'danger'
  return 'info'
}

function formatMem(gb: number): string {
  return gb >= 1024 ? `${(gb / 1024).toFixed(1)}T` : `${Math.round(gb)}G`
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await clusterApi.topology(props.clusterId)
    hosts.value = res.hosts || []
    groups.value = res.groups || []
    nodeCount.value = res.nodeCount || 0
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})
</script>

<style scoped lang="scss">
.topology {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 14px;
}

/* ---- 按主机 ---- */
.topo-host {
  border: 1px solid var(--tmp-border);
  border-radius: 8px;
  padding: 12px;
  background: #fafbfc;
  display: flex;
  flex-direction: column;
}

.host-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.host-id {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex-wrap: wrap;
}

.host-icon {
  color: var(--tmp-text-muted);
}

.host-name {
  font-weight: 600;
  font-size: 13px;
}

.host-ip {
  font-size: 12px;
}

.host-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.host-spec {
  margin-top: 6px;
  font-size: 12px;
  display: flex;
  gap: 10px;
}

.inst-list {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.inst-row {
  display: flex;
  align-items: center;
  gap: 6px;
  background: #fff;
  border: 1px solid var(--tmp-border);
  border-radius: 6px;
  padding: 6px 8px;
  font-size: 12.5px;
  min-width: 0;
}

.inst-comp {
  flex-shrink: 0;
  min-width: 62px;
  justify-content: center;
}

.inst-addr {
  flex-shrink: 0;
}

.port-extra {
  flex-shrink: 0;
  font-size: 12px;
}

.inst-ver {
  margin-left: auto;
  font-size: 12px;
  flex-shrink: 0;
}

.ml6 {
  margin-left: 6px;
}

/* ---- 按组件 ---- */
.topo-group {
  border: 1px solid var(--tmp-border);
  border-radius: 8px;
  padding: 12px;
  background: #fafbfc;
}

.topo-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}

.topo-component {
  font-weight: 600;
  font-size: 13px;
  letter-spacing: 0.5px;
}

.topo-nodes {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.topo-node {
  background: #fff;
  border: 1px solid var(--tmp-border);
  border-radius: 6px;
  padding: 8px 10px;
}

.node-line {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}

.node-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
  font-size: 12px;
}

.node-az {
  margin-top: 4px;
  font-size: 12px;
}
</style>
