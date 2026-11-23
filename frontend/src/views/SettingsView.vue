<template>
  <div class="page">
    <PageHeader title="系统设置" desc="采集参数、演示模式、凭据与系统信息" />

    <el-card shadow="never">
      <el-tabs v-model="activeTab">
        <!-- 采集配置 -->
        <el-tab-pane label="采集配置" name="collection">
          <el-form v-loading="loading" :model="collection" label-width="150px" style="max-width: 620px">
            <el-form-item label="采集周期（秒）">
              <el-input-number v-model="collection.intervalSec" :min="30" :max="86400" :disabled="readonly" />
              <span class="text-muted hint">30 ~ 86400</span>
            </el-form-item>
            <el-form-item label="单集群超时（秒）">
              <el-input-number v-model="collection.timeoutSec" :min="3" :max="60" :disabled="readonly" />
            </el-form-item>
            <el-form-item label="采集并发数">
              <el-input-number v-model="collection.concurrency" :min="1" :max="32" :disabled="readonly" />
            </el-form-item>
            <el-form-item label="失败重试次数">
              <el-input-number v-model="collection.retry" :min="0" :max="5" :disabled="readonly" />
            </el-form-item>
            <el-form-item label="SSH 命令超时（秒）">
              <el-input-number v-model="collection.sshTimeoutSec" :min="5" :max="300" :disabled="readonly" />
              <span class="text-muted hint">单条远端命令上限（如 tiup cluster display），5 ~ 300</span>
            </el-form-item>
            <el-form-item label="异常判定阈值">
              <el-input-number v-model="collection.clusterErrorStreak" :min="1" :max="20" :disabled="readonly" />
              <span class="text-muted hint">连续失败 N 次后标记为异常</span>
            </el-form-item>
            <el-form-item label="CPU 巡检阈值（%）">
              <el-input-number v-model="collection.thresholdCpuPct" :min="1" :max="100" :step="1" :disabled="readonly" />
            </el-form-item>
            <el-form-item label="磁盘巡检阈值（%）">
              <el-input-number v-model="collection.thresholdDiskPct" :min="1" :max="100" :step="1" :disabled="readonly" />
            </el-form-item>
            <el-form-item label="Token 有效期（小时）">
              <el-input-number v-model="collection.jwtTtlHours" :min="1" :max="720" :disabled="readonly" />
            </el-form-item>
            <el-form-item label="任务保留（天）">
              <el-input-number v-model="collection.retentionJobsDays" :min="1" :max="365" :disabled="readonly" />
            </el-form-item>
            <el-form-item label="审计保留（天）">
              <el-input-number v-model="collection.retentionAuditDays" :min="1" :max="3650" :disabled="readonly" />
            </el-form-item>
            <el-form-item v-if="auth.can('setting.manage')">
              <el-button type="primary" :loading="saving" @click="saveCollection">保存配置</el-button>
              <span class="text-muted hint">保存后对新发起的采集立即生效</span>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- 演示模式 -->
        <el-tab-pane label="演示模式" name="demo">
          <div v-loading="loading" class="demo-block">
            <el-alert
              :type="demo?.enabled ? 'warning' : 'info'"
              :closable="false"
              show-icon
              :title="demo?.enabled ? '当前使用内置演示数据（3 套假集群）' : '当前连接真实集群采集'"
              :description="`数据源：${demo?.dataSource || '—'}`"
            />
            <div class="demo-clusters">
              <span class="text-muted">内置集群：</span>
              <el-tag v-for="name in demo?.mockClusters || []" :key="name" size="small" effect="plain">
                {{ name }}
              </el-tag>
            </div>
            <el-switch
              :model-value="demo?.enabled"
              :disabled="demo?.forced || !auth.can('demo.toggle')"
              active-text="启用演示模式"
              @change="toggleDemo"
            />
            <p v-if="demo?.forced" class="text-muted">
              演示模式已由环境变量 <code>TMP_MOCK=true</code> 强制启用，界面不可切换。
            </p>
          </div>
        </el-tab-pane>

        <!-- 凭据管理 -->
        <el-tab-pane label="凭据管理" name="credentials">
          <el-table v-loading="loading" :data="credentials" size="small" empty-text="暂无凭据">
            <el-table-column prop="clusterName" label="集群" min-width="140" />
            <el-table-column label="OS 凭据" min-width="200">
              <template #default="{ row }">
                <span class="mono">{{ row.tiupUser }}@{{ row.tiupHost }}</span>
                <el-tag size="small" :type="row.hasSshPassword ? 'success' : 'info'" effect="plain" style="margin-left: 6px">
                  {{ row.hasSshPassword ? '已设置密码' : '未设置' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="DB 凭据" min-width="200">
              <template #default="{ row }">
                <span class="mono">{{ row.dbUser }}@{{ row.dbHost }}:{{ row.dbPort }}</span>
                <el-tag size="small" :type="row.hasDbPassword ? 'success' : 'info'" effect="plain" style="margin-left: 6px">
                  {{ row.hasDbPassword ? '已设置密码' : '未设置' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="更新时间" width="160">
              <template #default="{ row }">{{ formatTime(row.updatedAt) }}</template>
            </el-table-column>
            <el-table-column v-if="auth.can('credential.manage')" label="操作" width="160" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" @click="openCredential(row)">更新</el-button>
                <el-button link type="primary" @click="testCredential(row)">测试</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <!-- 关于 -->
        <el-tab-pane label="关于" name="about">
          <div v-loading="loading" class="about-block">
            <div class="detail-grid">
              <div class="detail-item"><span class="detail-label">版本</span><span class="detail-value">{{ about?.version }}</span></div>
              <div class="detail-item"><span class="detail-label">构建时间</span><span class="detail-value">{{ about?.buildTime || '—' }}</span></div>
              <div class="detail-item"><span class="detail-label">Commit</span><span class="detail-value mono">{{ about?.commit || '—' }}</span></div>
              <div class="detail-item"><span class="detail-label">元数据库</span><span class="detail-value">{{ about?.database }}</span></div>
              <div class="detail-item"><span class="detail-label">运行模式</span><span class="detail-value">{{ about?.mockMode ? '演示模式' : '真实采集' }}</span></div>
            </div>

            <h4 class="section-title mt-16">TiDB 版本兼容矩阵</h4>
            <el-table :data="about?.compatibility || []" size="small">
              <el-table-column prop="version" label="版本" width="100" />
              <el-table-column label="基础信息" width="100" align="center">
                <template #default="{ row }"><el-icon v-if="row.basicInfo" color="#67c23a"><Check /></el-icon></template>
              </el-table-column>
              <el-table-column label="TiDB 用户" width="110" align="center">
                <template #default="{ row }"><el-icon v-if="row.tidbUser" color="#67c23a"><Check /></el-icon></template>
              </el-table-column>
              <el-table-column label="Resource Group" width="140" align="center">
                <template #default="{ row }"><el-icon v-if="row.resourceGroup" color="#67c23a"><Check /></el-icon></template>
              </el-table-column>
              <el-table-column label="会话/Kill" width="120" align="center">
                <template #default="{ row }">
                  <el-icon v-if="row.session" color="#67c23a"><Check /></el-icon>
                </template>
              </el-table-column>
              <el-table-column label="参数查看/修改" width="140" align="center">
                <template #default="{ row }">
                  <el-icon v-if="row.variables" color="#67c23a"><Check /></el-icon>
                </template>
              </el-table-column>
              <el-table-column prop="note" label="说明" min-width="180" />
            </el-table>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- 更新凭据 -->
    <el-dialog v-model="credentialVisible" title="更新凭据" width="480px" :close-on-click-modal="false">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        class="mb-12"
        title="凭据需重新输入明文，服务端 AES-256-GCM 加密存储，接口永不明文返回。"
      />
      <el-form label-width="110px">
        <el-form-item label="SSH 密码">
          <el-input v-model="credentialForm.sshPassword" type="password" show-password placeholder="留空表示不修改" />
        </el-form-item>
        <el-form-item label="SSH 私钥">
          <el-input
            v-model="credentialForm.sshKey"
            type="textarea"
            :rows="3"
            placeholder="可选，粘贴私钥内容；留空表示不修改"
          />
        </el-form-item>
        <el-form-item label="DB 密码">
          <el-input v-model="credentialForm.dbPassword" type="password" show-password placeholder="留空表示不修改" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="credentialVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="saveCredential">保存</el-button>
      </template>
    </el-dialog>

    <!-- 连通性测试结果 -->
    <el-dialog v-model="probeVisible" title="连通性测试" width="560px">
      <ProbeResultList :results="probeResults" />
      <template #footer>
        <el-button type="primary" @click="probeVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Check } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import ProbeResultList from '@/components/ProbeResultList.vue'
import { settingApi } from '@/api'
import type { AboutInfo, CollectionSettings, CredentialView, DemoModeStatus, ProbeResult } from '@/api/types'
import { formatTime } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

const activeTab = ref('collection')
const loading = ref(false)
const saving = ref(false)
const submitting = ref(false)

const collection = ref<CollectionSettings>({
  intervalSec: 300,
  timeoutSec: 10,
  concurrency: 4,
  retry: 1,
  sshTimeoutSec: 30,
  jwtTtlHours: 8,
  idleTimeoutMin: 30,
  retentionJobsDays: 30,
  retentionAuditDays: 180,
  thresholdCpuPct: 80,
  thresholdDiskPct: 85,
  clusterErrorStreak: 3,
  mockMode: false,
  mockForced: false,
})

const demo = ref<DemoModeStatus | null>(null)
const credentials = ref<CredentialView[]>([])
const about = ref<AboutInfo | null>(null)

const credentialVisible = ref(false)
const credentialTarget = ref<CredentialView | null>(null)
const credentialForm = reactive({ sshPassword: '', sshKey: '', dbPassword: '' })
const probeVisible = ref(false)
const probeResults = ref<ProbeResult[]>([])

const readonly = ref(!auth.can('setting.manage'))

async function load(): Promise<void> {
  loading.value = true
  try {
    const tasks: Promise<void>[] = [
      settingApi.collection().then((res) => {
        collection.value = res
      }),
      settingApi.demoMode().then((res) => {
        demo.value = res
      }),
      settingApi.about().then((res) => {
        about.value = res
      }),
    ]
    if (auth.can('credential.view')) {
      tasks.push(settingApi.credentials().then((res) => {
        credentials.value = res
      }))
    }
    await Promise.all(tasks)
  } finally {
    loading.value = false
  }
}

async function saveCollection(): Promise<void> {
  saving.value = true
  try {
    collection.value = await settingApi.updateCollection(collection.value)
    ElMessage.success('采集配置已保存')
  } finally {
    saving.value = false
  }
}

async function toggleDemo(enabled: boolean | string | number): Promise<void> {
  const next = Boolean(enabled)
  const ok = await ElMessageBox.confirm(
    next
      ? '启用演示模式后将展示内置假数据，真实集群数据不再采集。确认启用？'
      : '关闭演示模式后将立即用真实凭据采集集群，请确保凭据正确。确认关闭？',
    '切换采集模式',
    { type: 'warning' },
  ).catch(() => false)
  if (!ok) {
    return
  }
  demo.value = await settingApi.setDemoMode(next)
  ElMessage.success(next ? '已切换到演示模式' : '已切换到真实采集模式')
}

function openCredential(row: CredentialView): void {
  credentialTarget.value = row
  Object.assign(credentialForm, { sshPassword: '', sshKey: '', dbPassword: '' })
  credentialVisible.value = true
}

async function saveCredential(): Promise<void> {
  if (!credentialTarget.value) {
    return
  }
  if (!credentialForm.sshPassword && !credentialForm.sshKey && !credentialForm.dbPassword) {
    ElMessage.warning('请至少填写一项要更新的凭据')
    return
  }
  submitting.value = true
  try {
    await settingApi.updateCredential(credentialTarget.value.clusterId, {
      sshPassword: credentialForm.sshPassword || undefined,
      sshKey: credentialForm.sshKey || undefined,
      dbPassword: credentialForm.dbPassword || undefined,
    })
    ElMessage.success('凭据已更新')
    credentialVisible.value = false
    await load()
  } finally {
    submitting.value = false
  }
}

async function testCredential(row: CredentialView): Promise<void> {
  const res = await settingApi.testCredential(row.clusterId)
  probeResults.value = res.results || []
  probeVisible.value = true
}

onMounted(() => {
  void load()
})
</script>

<style scoped lang="scss">
.hint {
  margin-left: 10px;
  font-size: 12px;
}

.demo-block {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 720px;
}

.demo-clusters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.section-title {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 10px;
}

.about-block {
  max-width: 900px;
}
</style>
