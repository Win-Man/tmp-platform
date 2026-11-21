<template>
  <div class="page">
    <PageHeader title="巡检中心" desc="内置六大类 28 项最佳实践检查，支持多集群并发巡检并生成报告">
      <template #actions>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="auth.can('inspect.run')" type="primary" :icon="Aim" @click="openStart">
          发起巡检
        </el-button>
      </template>
    </PageHeader>

    <el-row :gutter="16">
      <el-col :xs="24" :md="10">
        <el-card shadow="never" header="巡检模板">
          <div v-loading="loading">
            <div v-for="tpl in templates" :key="tpl.id" class="template-card">
              <div class="template-head">
                <div>
                  <span class="template-name">{{ tpl.name }}</span>
                  <el-tag v-if="tpl.builtin" size="small" effect="plain" type="info" style="margin-left: 6px">内置</el-tag>
                </div>
                <el-tag size="small" effect="plain">{{ tpl.itemCount }} 项</el-tag>
              </div>
              <p class="text-muted template-desc">{{ tpl.description }}</p>
              <div class="template-cats">
                <el-tag
                  v-for="cat in tpl.categories || []"
                  :key="cat.category"
                  size="small"
                  effect="plain"
                  type="success"
                >
                  {{ cat.label }} {{ cat.count }}
                </el-tag>
              </div>
              <div class="mt-12">
                <el-button size="small" @click="openItems(tpl)">查看检查项</el-button>
                <el-button v-if="auth.can('inspect.run')" size="small" type="primary" plain @click="openStart(tpl)">
                  发起巡检
                </el-button>
              </div>
            </div>
          </div>
        </el-card>
      </el-col>

      <el-col :xs="24" :md="14">
        <el-card shadow="never" header="最近巡检记录">
          <el-table v-loading="loading" :data="recentRuns" size="small" empty-text="暂无巡检记录">
            <el-table-column prop="runNo" label="运行号" width="160" />
            <el-table-column prop="clusterName" label="集群" min-width="130" />
            <el-table-column label="结论" width="110">
              <template #default="{ row }"><StatusTag :status="row.conclusion" kind="conclusion" /></template>
            </el-table-column>
            <el-table-column label="通过率" width="100">
              <template #default="{ row }">{{ row.passRate }}%</template>
            </el-table-column>
            <el-table-column label="严重/警告" width="110">
              <template #default="{ row }">
                <el-tag type="danger" size="small" effect="plain">{{ row.criticalCount }}</el-tag>
                <el-tag type="warning" size="small" effect="plain" style="margin-left: 4px">{{ row.warningCount }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="执行时间" width="150">
              <template #default="{ row }">{{ formatTime(row.finishedAt || row.createdAt, 'MM-DD HH:mm:ss') }}</template>
            </el-table-column>
            <el-table-column label="操作" width="90">
              <template #default="{ row }">
                <el-button link type="primary" @click="openRun(row.id)">详情</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>

    <!-- 发起巡检 -->
    <el-dialog v-model="startVisible" title="发起巡检" width="560px" :close-on-click-modal="false">
      <el-form label-width="100px">
        <el-form-item label="巡检模板">
          <el-select v-model="startForm.templateId" style="width: 100%">
            <el-option v-for="tpl in templates" :key="tpl.id" :label="tpl.name" :value="tpl.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="目标集群">
          <el-select v-model="startForm.clusterIds" multiple filterable style="width: 100%" placeholder="可多选">
            <el-option v-for="c in clusters" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="每个集群各建一个巡检任务，可在任务中心查看执行过程"
      />
      <template #footer>
        <el-button @click="startVisible = false">取消</el-button>
        <el-button type="primary" :loading="starting" @click="submitStart">开始巡检</el-button>
      </template>
    </el-dialog>

    <!-- 检查项 -->
    <el-drawer v-model="itemsVisible" :title="`检查项 · ${currentTemplate?.name || ''}`" size="720px">
      <el-table :data="items" size="small" empty-text="暂无检查项">
        <el-table-column prop="code" label="编号" width="90" />
        <el-table-column prop="name" label="检查项" min-width="180" />
        <el-table-column label="分类" width="110">
          <template #default="{ row }">{{ row.categoryLabel || enums.label('inspectCategory', row.category) }}</template>
        </el-table-column>
        <el-table-column label="方式" width="90">
          <template #default="{ row }">{{ enums.label('checkMethod', row.checkMethod) }}</template>
        </el-table-column>
        <el-table-column label="级别" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.severity === 'critical' ? 'danger' : 'warning'" effect="plain">
              {{ row.severity === 'critical' ? '严重' : '警告' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="expect" label="期望" min-width="200" show-overflow-tooltip />
      </el-table>
    </el-drawer>

    <!-- 巡检运行详情 -->
    <el-drawer v-model="runVisible" :title="`巡检运行 · ${runDetail?.run.runNo || ''}`" size="820px">
      <div v-if="runDetail" v-loading="runLoading">
        <div class="run-head">
          <div>
            <div class="run-cluster">{{ runDetail.run.clusterName }}</div>
            <div class="text-muted">
              {{ runDetail.template.name }} · {{ formatTime(runDetail.run.finishedAt || runDetail.run.createdAt) }}
            </div>
          </div>
          <div class="run-actions">
            <StatusTag :status="runDetail.run.conclusion" kind="conclusion" />
            <el-button
              v-if="runDetail.reportId"
              size="small"
              type="primary"
              plain
              @click="router.push(`/reports/${runDetail.reportId}`)"
            >
              查看报告
            </el-button>
          </div>
        </div>

        <el-row :gutter="12" class="mt-12">
          <el-col :span="6"><div class="mini-stat"><div class="v">{{ runDetail.run.totalCount }}</div><div class="l">检查项</div></div></el-col>
          <el-col :span="6"><div class="mini-stat danger"><div class="v">{{ runDetail.run.criticalCount }}</div><div class="l">严重</div></div></el-col>
          <el-col :span="6"><div class="mini-stat warning"><div class="v">{{ runDetail.run.warningCount }}</div><div class="l">警告</div></div></el-col>
          <el-col :span="6"><div class="mini-stat success"><div class="v">{{ runDetail.run.passRate }}%</div><div class="l">通过率</div></div></el-col>
        </el-row>

        <el-table :data="runDetail.results" size="small" class="mt-12" empty-text="暂无明细">
          <el-table-column prop="itemCode" label="编号" width="90" />
          <el-table-column prop="itemName" label="检查项" min-width="170" />
          <el-table-column label="级别" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="levelTagType(row.level)" effect="plain">
                {{ enums.label('inspectLevel', row.level) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="actual" label="实际" min-width="200" show-overflow-tooltip />
          <el-table-column prop="suggestion" label="建议" min-width="220" show-overflow-tooltip />
        </el-table>
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Aim, Refresh } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import { clusterApi, inspectApi, type InspectRunDetail } from '@/api'
import type { ClusterOption, InspectItem, InspectReport, InspectTemplate } from '@/api/types'
import { formatTime } from '@/utils/format'
import { queryString } from '@/utils/query'
import { useAuthStore } from '@/stores/auth'
import { levelTagType, useEnumStore } from '@/stores/enums'

const auth = useAuthStore()
const enums = useEnumStore()
const router = useRouter()
const route = useRoute()

const loading = ref(false)
const templates = ref<InspectTemplate[]>([])
const recentRuns = ref<InspectReport[]>([])
const clusters = ref<ClusterOption[]>([])

const startVisible = ref(false)
const starting = ref(false)
const startForm = reactive({ templateId: 0, clusterIds: [] as number[] })

const itemsVisible = ref(false)
const currentTemplate = ref<InspectTemplate | null>(null)
const items = ref<InspectItem[]>([])

const runVisible = ref(false)
const runLoading = ref(false)
const runDetail = ref<InspectRunDetail | null>(null)

async function load(): Promise<void> {
  loading.value = true
  try {
    const [tplRes, reportRes] = await Promise.all([
      inspectApi.templates(),
      inspectApi.reports({ page: 1, pageSize: 10 }),
    ])
    templates.value = tplRes.list || []
    recentRuns.value = reportRes.list || []
    if (!startForm.templateId && templates.value.length > 0) {
      startForm.templateId = templates.value[0].id
    }
  } finally {
    loading.value = false
  }
}

function openStart(tpl?: InspectTemplate): void {
  if (tpl) {
    startForm.templateId = tpl.id
  }
  startForm.clusterIds = []
  startVisible.value = true
}

async function submitStart(): Promise<void> {
  if (startForm.clusterIds.length === 0) {
    ElMessage.warning('请选择至少一个集群')
    return
  }
  starting.value = true
  try {
    const res = await inspectApi.startRuns(startForm.clusterIds, startForm.templateId)
    ElMessage.success(`已创建 ${res.results.length} 个巡检任务`)
    startVisible.value = false
    await load()
    if (res.results.length === 1) {
      void router.push(`/jobs/${res.results[0].jobId}`)
    }
  } finally {
    starting.value = false
  }
}

async function openItems(tpl: InspectTemplate): Promise<void> {
  currentTemplate.value = tpl
  itemsVisible.value = true
  const res = await inspectApi.template(tpl.id)
  items.value = res.items || []
}

async function openRun(id: number): Promise<void> {
  runVisible.value = true
  runLoading.value = true
  try {
    runDetail.value = await inspectApi.run(id)
  } finally {
    runLoading.value = false
  }
}

onMounted(async () => {
  await enums.ensure()
  clusters.value = await clusterApi.options().catch(() => [])
  await load()
  const runParam = route.query.run
  if (runParam) {
    await openRun(Number(runParam))
  }
  // 从集群详情「巡检」页跳转过来时，预选该集群并直接打开发起弹窗。
  const presetClusterId = Number(queryString(route.query, 'clusterId'))
  if (presetClusterId > 0 && auth.can('inspect.run')) {
    startForm.clusterIds = [presetClusterId]
    startVisible.value = true
  }
})
</script>

<style scoped lang="scss">
.template-card {
  border: 1px solid var(--tmp-border);
  border-radius: 8px;
  padding: 14px;
  margin-bottom: 12px;
  background: #fafbfc;

  &:last-child {
    margin-bottom: 0;
  }
}

.template-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.template-name {
  font-weight: 600;
}

.template-desc {
  font-size: 12.5px;
  margin: 8px 0;
  line-height: 1.6;
}

.template-cats {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.run-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
}

.run-cluster {
  font-size: 16px;
  font-weight: 600;
}

.run-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.mini-stat {
  border: 1px solid var(--tmp-border);
  border-radius: 6px;
  padding: 10px 12px;
  text-align: center;

  .v {
    font-size: 20px;
    font-weight: 600;
  }

  .l {
    font-size: 12px;
    color: var(--tmp-text-muted);
  }

  &.danger .v {
    color: #f56c6c;
  }

  &.warning .v {
    color: #e6a23c;
  }

  &.success .v {
    color: #67c23a;
  }
}
</style>
