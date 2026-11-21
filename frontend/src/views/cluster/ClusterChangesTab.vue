<template>
  <div v-loading="loading">
    <div class="table-toolbar">
      <div class="toolbar-left text-muted">来自审计日志与任务记录的变更时间线（最近 50 条）</div>
      <div class="toolbar-right">
        <el-button size="small" @click="load">刷新</el-button>
      </div>
    </div>
    <el-empty v-if="!loading && rows.length === 0" description="暂无变更记录" />
    <el-timeline v-else>
      <el-timeline-item
        v-for="item in rows"
        :key="item.id"
        :timestamp="formatTime(item.createdAt)"
        placement="top"
        :type="item.result === 'success' ? 'primary' : 'danger'"
      >
        <div class="change-line">
          <strong>{{ actionLabel(item.action) }}</strong>
          <el-tag size="small" :type="item.result === 'success' ? 'success' : 'danger'" effect="plain">
            {{ item.result === 'success' ? '成功' : '失败' }}
          </el-tag>
          <span class="text-muted">操作者：{{ item.username || '—' }}</span>
          <router-link v-if="item.jobId" :to="`/jobs/${item.jobId}`" class="link">任务 #{{ item.jobId }}</router-link>
        </div>
        <div v-if="item.detail" class="text-muted mono change-detail">{{ item.detail }}</div>
      </el-timeline-item>
    </el-timeline>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { clusterApi, type ClusterChangeRow } from '@/api'
import { formatTime } from '@/utils/format'

const props = defineProps<{ clusterId: number }>()

const loading = ref(false)
const rows = ref<ClusterChangeRow[]>([])

/** 审计 action → 中文说明。 */
function actionLabel(action: string): string {
  const map: Record<string, string> = {
    'cluster.takeover': '接管集群',
    'cluster.update': '编辑纳管信息',
    'cluster.refresh': '触发采集',
    'cluster.delete': '移除纳管',
    'tidb_user.create': '创建 TiDB 用户',
    'tidb_user.delete': '删除 TiDB 用户',
    'tidb_user.reset_password': '重置 TiDB 用户密码',
    'tidb_user.grant': '变更用户权限',
    'resource_group.bind': '绑定 Resource Group',
    'resource_group.unbind': '解绑 Resource Group',
    'session.kill': 'Kill 会话',
    'variable.update': '修改全局变量',
    'inspection.run': '发起巡检',
    'inspection.download': '下载巡检报告',
    'job.collect': '采集任务',
    'job.inspect': '巡检任务',
    'job.ssh_check': 'SSH 连通性检测',
  }
  return map[action] || action
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await clusterApi.changes(props.clusterId)
    rows.value = res.list || []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})
</script>

<style scoped lang="scss">
.change-line {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 13px;
}

.change-detail {
  margin-top: 4px;
  word-break: break-all;
}

.link {
  color: #2f6fed;
  text-decoration: none;
}
</style>
