<template>
  <div class="page">
    <PageHeader :title="`任务 #${jobId}`" :desc="job ? `${job.typeLabel || job.type} · ${job.targetLabel || ''}` : ''">
      <template #actions>
        <el-button :icon="ArrowLeft" @click="router.push('/jobs')">返回列表</el-button>
        <el-button v-if="canRetry" @click="retry">重试</el-button>
        <el-button v-if="canCancel" type="danger" plain @click="cancel">取消任务</el-button>
      </template>
    </PageHeader>

    <el-row :gutter="16">
      <el-col :xs="24" :md="10">
        <el-card shadow="never" header="任务概要" v-loading="loading">
          <div class="detail-grid" v-if="job">
            <div class="detail-item"><span class="detail-label">状态</span><span class="detail-value"><StatusTag :status="job.status" kind="job" /></span></div>
            <div class="detail-item"><span class="detail-label">进度</span><span class="detail-value">{{ job.progress }}%</span></div>
            <div class="detail-item"><span class="detail-label">当前步骤</span><span class="detail-value">{{ job.currentStep || '—' }}</span></div>
            <div class="detail-item"><span class="detail-label">对象</span><span class="detail-value">{{ job.targetLabel || '—' }}</span></div>
            <div class="detail-item"><span class="detail-label">发起人</span><span class="detail-value">{{ job.createdByName || '—' }}</span></div>
            <div class="detail-item"><span class="detail-label">开始时间</span><span class="detail-value">{{ formatTime(job.startedAt) }}</span></div>
            <div class="detail-item"><span class="detail-label">结束时间</span><span class="detail-value">{{ formatTime(job.finishedAt) }}</span></div>
            <div class="detail-item"><span class="detail-label">耗时</span><span class="detail-value">{{ formatDuration(job.durationMs) }}</span></div>
            <div v-if="job.retryOf" class="detail-item"><span class="detail-label">重试自</span><span class="detail-value">#{{ job.retryOf }}</span></div>
          </div>
          <el-alert
            v-if="job?.errorSummary"
            type="error"
            show-icon
            :closable="false"
            class="mt-12"
            :title="job.errorSummary"
          />
          <el-progress :percentage="job?.progress || 0" :stroke-width="12" class="mt-12" />
        </el-card>

        <el-card shadow="never" header="步骤" class="mt-16">
          <el-timeline>
            <el-timeline-item
              v-for="step in steps"
              :key="step.id"
              :type="stepType(step.status)"
              :hollow="step.status === 'pending'"
              :timestamp="formatTime(step.finishedAt || step.startedAt, 'HH:mm:ss')"
              placement="top"
            >
              <div class="step-line">
                <span>{{ step.name }}</span>
                <el-tag size="small" effect="plain" :type="stepTag(step.status)">
                  {{ enums.label('stepStatus', step.status) }}
                </el-tag>
              </div>
              <div v-if="step.error" class="step-error">{{ step.error }}</div>
            </el-timeline-item>
          </el-timeline>
          <el-empty v-if="steps.length === 0" description="暂无步骤" :image-size="60" />
        </el-card>
      </el-col>

      <el-col :xs="24" :md="14">
        <el-card shadow="never">
          <template #header>
            <div class="log-header">
              <span>执行日志</span>
              <div>
                <el-switch v-model="autoScroll" active-text="自动滚动" size="small" />
              </div>
            </div>
          </template>
          <div ref="logBox" class="log-box">
            <div v-if="logs.length === 0" class="empty-block">暂无日志</div>
            <div v-for="log in logs" :key="log.seq" class="log-line" :class="`log-${log.level}`">
              <span class="log-time">{{ formatTime(log.at, 'HH:mm:ss') }}</span>
              <span class="log-level">{{ log.level.toUpperCase() }}</span>
              <span class="log-msg">{{ log.message }}</span>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import { jobApi } from '@/api'
import type { JobLog, JobRow, JobStep } from '@/api/types'
import { formatDuration, formatTime } from '@/utils/format'
import { useEnumStore } from '@/stores/enums'

const route = useRoute()
const router = useRouter()
const enums = useEnumStore()

const jobId = ref(Number(route.params.id) || 0)
const job = ref<JobRow | null>(null)
const steps = ref<JobStep[]>([])
const logs = ref<JobLog[]>([])
const loading = ref(false)
const autoScroll = ref(true)
const logBox = ref<HTMLDivElement | null>(null)
let timer: number | null = null
let lastSeq = 0

const canRetry = computed(() => job.value && ['failed', 'interrupted', 'canceled'].includes(job.value.status))
const canCancel = computed(() => job.value && ['pending', 'running'].includes(job.value.status))

function stepType(status: string): 'success' | 'warning' | 'danger' | 'primary' | 'info' {
  switch (status) {
    case 'success':
      return 'success'
    case 'running':
      return 'primary'
    case 'failed':
      return 'danger'
    case 'skipped':
      return 'info'
    default:
      return 'warning'
  }
}

function stepTag(status: string): 'success' | 'warning' | 'danger' | 'info' | 'primary' {
  switch (status) {
    case 'success':
      return 'success'
    case 'running':
      return 'primary'
    case 'failed':
      return 'danger'
    default:
      return 'info'
  }
}

async function loadAll(): Promise<void> {
  loading.value = true
  try {
    const [jobRes, stepRes] = await Promise.all([jobApi.get(jobId.value), jobApi.steps(jobId.value)])
    job.value = jobRes
    steps.value = stepRes.list || []
  } finally {
    loading.value = false
  }
  await loadLogs()
}

async function loadLogs(): Promise<void> {
  const res = await jobApi.logs(jobId.value, lastSeq)
  const list = res.list || []
  if (list.length > 0) {
    logs.value.push(...list)
    lastSeq = list[list.length - 1].seq
    if (autoScroll.value) {
      await nextTick()
      if (logBox.value) {
        logBox.value.scrollTop = logBox.value.scrollHeight
      }
    }
  }
}

function startTimer(): void {
  stopTimer()
  timer = window.setInterval(async () => {
    if (!job.value) {
      return
    }
    if (job.value.status === 'pending' || job.value.status === 'running') {
      const prevStatus = job.value.status
      await loadAll()
      if (prevStatus !== job.value.status) {
        ElMessage.success(`任务状态更新：${enums.label('jobStatus', job.value.status)}`)
      }
    } else {
      await loadLogs()
    }
  }, 3000)
}

function stopTimer(): void {
  if (timer !== null) {
    window.clearInterval(timer)
    timer = null
  }
}

async function retry(): Promise<void> {
  const res = await jobApi.retry(jobId.value)
  ElMessage.success(`重试任务 #${res.jobId} 已创建`)
  void router.push(`/jobs/${res.jobId}`)
}

async function cancel(): Promise<void> {
  const ok = await ElMessageBox.confirm('确认取消该任务？', '取消任务', { type: 'warning' }).catch(() => false)
  if (!ok) {
    return
  }
  await jobApi.cancel(jobId.value)
  ElMessage.success('已请求取消')
  await loadAll()
}

onMounted(async () => {
  await enums.ensure()
  await loadAll()
  startTimer()
})

onBeforeUnmount(() => {
  stopTimer()
})
</script>

<style scoped lang="scss">
.step-line {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.step-error {
  color: #f56c6c;
  font-size: 12px;
  margin-top: 4px;
  word-break: break-all;
}

.log-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.log-box {
  height: 620px;
  overflow-y: auto;
  background: #0f172a;
  border-radius: 6px;
  padding: 12px;
  font-family: 'SFMono-Regular', Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.7;
  color: #cbd5e1;
}

.log-line {
  display: flex;
  gap: 10px;

  &.log-error .log-msg {
    color: #fca5a5;
  }

  &.log-warn .log-msg {
    color: #fcd34d;
  }
}

.log-time {
  color: #64748b;
  flex-shrink: 0;
}

.log-level {
  width: 46px;
  flex-shrink: 0;
  color: #7dd3fc;
}

.log-msg {
  word-break: break-all;
  white-space: pre-wrap;
}
</style>
