<template>
  <div class="page" v-loading="loading">
    <PageHeader
      :title="`巡检报告 · ${report?.reportNo || ''}`"
      :desc="report ? `${report.clusterName} · ${report.templateName} · ${formatTime(report.executedAt)}` : ''"
    >
      <template #actions>
        <el-button :icon="ArrowLeft" @click="router.push('/reports')">返回列表</el-button>
        <el-button v-if="auth.can('report.download')" @click="download('md')">下载 Markdown</el-button>
        <el-button v-if="auth.can('report.download')" type="primary" @click="download('html')">
          下载 HTML
        </el-button>
      </template>
    </PageHeader>

    <template v-if="report">
      <el-row :gutter="16">
        <el-col :xs="12" :md="6">
          <div class="stat-card">
            <div class="stat-label">巡检结论</div>
            <div class="stat-value"><StatusTag :status="report.conclusion" kind="conclusion" /></div>
            <div class="stat-extra">通过率 {{ report.passRate }}%</div>
          </div>
        </el-col>
        <el-col :xs="12" :md="6">
          <div class="stat-card">
            <div class="stat-label">严重问题</div>
            <div class="stat-value" style="color: #f56c6c">{{ report.criticalCount }}</div>
            <div class="stat-extra">需优先处理</div>
          </div>
        </el-col>
        <el-col :xs="12" :md="6">
          <div class="stat-card">
            <div class="stat-label">警告</div>
            <div class="stat-value" style="color: #e6a23c">{{ report.warningCount }}</div>
            <div class="stat-extra">建议优化</div>
          </div>
        </el-col>
        <el-col :xs="12" :md="6">
          <div class="stat-card">
            <div class="stat-label">通过项</div>
            <div class="stat-value" style="color: #67c23a">{{ report.passCount }}</div>
            <div class="stat-extra">共 {{ report.totalCount }} 项检查</div>
          </div>
        </el-col>
      </el-row>

      <el-card shadow="never" class="mt-16">
        <el-tabs v-model="activeTab">
          <el-tab-pane label="报告正文" name="html">
            <iframe class="report-frame" :srcdoc="report.contentHtml || fallbackHtml" sandbox="" />
          </el-tab-pane>
          <el-tab-pane label="问题明细" name="results">
            <el-table :data="problems" size="small" empty-text="没有严重或警告项 🎉">
              <el-table-column prop="itemCode" label="编号" width="100" />
              <el-table-column prop="itemName" label="检查项" min-width="180" />
              <el-table-column label="级别" width="100">
                <template #default="{ row }">
                  <el-tag size="small" :type="levelTagType(row.level)" effect="plain">
                    {{ enums.label('inspectLevel', row.level) }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="actual" label="实际" min-width="220" show-overflow-tooltip />
              <el-table-column prop="expect" label="期望" min-width="180" show-overflow-tooltip />
              <el-table-column prop="suggestion" label="建议" min-width="260" show-overflow-tooltip />
            </el-table>
          </el-tab-pane>
          <el-tab-pane label="全部检查项" name="all">
            <el-table :data="report.results || []" size="small" max-height="560">
              <el-table-column prop="itemCode" label="编号" width="100" />
              <el-table-column prop="itemName" label="检查项" min-width="180" />
              <el-table-column label="级别" width="100">
                <template #default="{ row }">
                  <el-tag size="small" :type="levelTagType(row.level)" effect="plain">
                    {{ enums.label('inspectLevel', row.level) }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="actual" label="实际" min-width="240" show-overflow-tooltip />
            </el-table>
          </el-tab-pane>
        </el-tabs>
      </el-card>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import { downloadFile, inspectApi, type InspectReportDetail, type InspectResult } from '@/api'
import { formatTime } from '@/utils/format'
import { levelTagType, useEnumStore } from '@/stores/enums'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const enums = useEnumStore()

const loading = ref(false)
const report = ref<InspectReportDetail | null>(null)
const activeTab = ref('html')

const problems = computed<InspectResult[]>(() =>
  (report.value?.results || []).filter((r) => r.level === 'critical' || r.level === 'warning'),
)

const fallbackHtml = computed(() => {
  if (!report.value?.contentMd) {
    return '<p style="font-family:sans-serif;color:#888">报告内容为空</p>'
  }
  return `<pre style="white-space:pre-wrap;font-family:monospace">${report.value.contentMd.replace(/</g, '&lt;')}</pre>`
})

async function load(): Promise<void> {
  loading.value = true
  try {
    report.value = await inspectApi.report(Number(route.params.id))
  } finally {
    loading.value = false
  }
}

async function download(format: 'md' | 'html'): Promise<void> {
  if (!report.value) {
    return
  }
  await downloadFile(inspectApi.downloadUrl(report.value.id, format), `${report.value.reportNo}.${format}`)
  ElMessage.success('下载已开始')
}

onMounted(async () => {
  await enums.ensure()
  await load()
})
</script>

<style scoped lang="scss">
.report-frame {
  width: 100%;
  height: 640px;
  border: 1px solid var(--tmp-border);
  border-radius: 6px;
  background: #fff;
}
</style>
