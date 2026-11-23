<template>
  <div>
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-select v-model="filters.component" placeholder="组件" clearable style="width: 140px" @change="search">
          <el-option v-for="c in componentOptions" :key="c" :label="c" :value="c" />
        </el-select>
        <el-select v-model="filters.status" placeholder="状态" clearable style="width: 130px" @change="search">
          <el-option v-for="o in enums.group('instanceStatus')" :key="o.value" :label="o.label" :value="o.value" />
        </el-select>
        <el-button @click="search">查询</el-button>
      </div>
      <div class="toolbar-right text-muted">共 {{ total }} 个实例</div>
    </div>

    <el-table v-loading="loading" :data="rows" size="small" empty-text="暂无实例数据">
      <el-table-column prop="component" label="组件" width="120" />
      <el-table-column label="地址" min-width="180">
        <template #default="{ row }">
          <span class="mono">{{ row.hostIp }}:{{ row.port }}</span>
          <el-tooltip
            v-if="row.rawHostIp"
            :content="`已按地址映射显示可达地址，集群内网地址为 ${row.rawHostIp}`"
            placement="top"
          >
            <el-tag size="small" type="success" effect="plain" style="margin-left: 6px">已映射</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="statusPort" label="状态端口" width="110" />
      <el-table-column label="状态" width="110">
        <template #default="{ row }"><StatusTag :status="row.status" kind="instance" /></template>
      </el-table-column>
      <el-table-column prop="role" label="角色" width="110" />
      <el-table-column prop="version" label="版本" width="110" />
      <el-table-column prop="az" label="可用区" width="110">
        <template #default="{ row }">{{ row.az || '—' }}</template>
      </el-table-column>
      <el-table-column prop="deployDir" label="部署目录" min-width="200" show-overflow-tooltip />
    </el-table>

    <el-pagination
      class="mt-12"
      layout="total, prev, pager, next"
      :total="total"
      :page-size="filters.pageSize"
      :current-page="filters.page"
      @current-change="onPageChange"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import StatusTag from '@/components/StatusTag.vue'
import { clusterApi } from '@/api'
import type { InstanceRow } from '@/api/types'
import { useEnumStore } from '@/stores/enums'
import { queryInt, queryString, syncQuery } from '@/utils/query'

const props = defineProps<{ clusterId: number }>()

const route = useRoute()
const router = useRouter()
const enums = useEnumStore()
const loading = ref(false)
const rows = ref<InstanceRow[]>([])
const total = ref(0)
const filters = reactive({ component: '', status: '', page: 1, pageSize: 20 })

const componentOptions = computed(() => Array.from(new Set(rows.value.map((r) => r.component))).sort())

/** 把筛选条件写回 URL，支持「实例异常 N」徽标带 status=abnormal 直达。 */
function syncUrl(): void {
  syncQuery(router, route.query, {
    component: filters.component,
    status: filters.status,
    page: filters.page > 1 ? filters.page : undefined,
  })
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await clusterApi.instances(props.clusterId, {
      page: filters.page,
      pageSize: filters.pageSize,
      component: filters.component || undefined,
      status: filters.status || undefined,
    })
    rows.value = res.list || []
    total.value = res.total || 0
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

onMounted(async () => {
  await enums.ensure()
  // 从 URL 恢复筛选（徽标跳转 / 刷新 / 分享链接）。
  filters.component = queryString(route.query, 'component')
  filters.status = queryString(route.query, 'status')
  filters.page = queryInt(route.query, 'page', 1)
  await load()
})
</script>
