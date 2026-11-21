<template>
  <div>
    <el-alert
      type="info"
      show-icon
      :closable="false"
      class="mb-12"
      title="性能趋势依赖 Prometheus 监控数据源。当前版本未接入，接口 /clusters/:id/metrics 返回 40901（能力不支持）。"
    />

    <el-empty description="未接入监控系统，暂无性能趋势数据">
      <el-button v-if="auth.can('setting.view')" @click="router.push('/settings')">前往系统设置</el-button>
      <el-button @click="router.push({ name: 'cluster-instances', params: { id: props.clusterId } })">
        查看实例状态
      </el-button>
    </el-empty>

    <h4 class="section-title">当前版本支持的可用观测手段</h4>
    <el-table :data="alternatives" size="small" border>
      <el-table-column prop="scene" label="观测场景" width="200" />
      <el-table-column prop="entry" label="入口" min-width="240" />
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

// clusterId 由子路由 props 注入：本页不请求后端，仅用于跳转到实例页时保留上下文。
const props = defineProps<{ clusterId: number }>()

const router = useRouter()
const auth = useAuthStore()

const alternatives = [
  { scene: '实例存活与版本', entry: '「实例」页：按组件筛选实例状态（up / down / unhealthy）' },
  { scene: '容量水位', entry: '「主机管理」页：CPU / 内存 / 磁盘使用率进度条' },
  { scene: '最近变更与采集结果', entry: '「变更记录」页 + 「任务中心」采集日志' },
  { scene: '配置与健康检查', entry: '「巡检中心」：内置 28 项巡检覆盖配置与高可用' },
]
</script>

<style scoped lang="scss">
.section-title {
  font-size: 14px;
  font-weight: 600;
  margin: 20px 0 12px;
}
</style>
