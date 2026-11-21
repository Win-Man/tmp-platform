<template>
  <div v-loading="loading">
    <div class="table-toolbar">
      <div class="toolbar-left text-muted">
        共 {{ groups.length }} 类组件 / {{ totalNodes }} 个实例（数据来自 SSH + MySQL + PD 三通道归一化结果）
      </div>
      <div class="toolbar-right">
        <el-button size="small" :icon="Refresh" @click="load">刷新</el-button>
      </div>
    </div>

    <el-empty v-if="!loading && groups.length === 0" description="暂无拓扑数据，请先执行采集" />

    <div class="topology">
      <div v-for="group in groups" :key="group.component" class="topo-group">
        <div class="topo-head">
          <span class="topo-component">{{ group.component.toUpperCase() }}</span>
          <el-tag size="small" effect="plain">{{ group.nodes.length }} 实例</el-tag>
        </div>
        <div class="topo-nodes">
          <div v-for="node in group.nodes" :key="node.id" class="topo-node">
            <div class="node-line">
              <span class="status-dot" :class="node.status === 'up' ? 'dot-success' : 'dot-danger'" />
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
import { Refresh } from '@element-plus/icons-vue'
import { clusterApi } from '@/api'
import type { TopologyGroup } from '@/api/types'

const props = defineProps<{ clusterId: number }>()

const loading = ref(false)
const groups = ref<TopologyGroup[]>([])

const totalNodes = computed(() => groups.value.reduce((sum, g) => sum + (g.nodes?.length || 0), 0))

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await clusterApi.topology(props.clusterId)
    groups.value = res.groups || []
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
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 14px;
}

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
