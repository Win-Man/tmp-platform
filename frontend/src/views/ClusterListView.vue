<template>
  <div class="page">
    <PageHeader title="集群管理" desc="纳管 TiUP 部署的 TiDB 集群，查看拓扑、实例与采集状态">
      <template #actions>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新列表</el-button>
        <el-button
          v-if="auth.can('cluster.refresh')"
          :disabled="selection.length === 0"
          @click="batchRefresh"
        >
          批量采集（{{ selection.length }}）
        </el-button>
        <el-button v-if="auth.can('cluster.takeover')" type="primary" :icon="Plus" @click="openTakeover">
          接管集群
        </el-button>
      </template>
    </PageHeader>

    <el-card shadow="never">
      <div class="table-toolbar">
        <div class="toolbar-left">
          <el-input
            v-model="filters.keyword"
            placeholder="集群名 / 主机 / 版本"
            clearable
            style="width: 220px"
            :prefix-icon="Search"
            @keyup.enter="search"
            @clear="search"
          />
          <el-select v-model="filters.status" placeholder="健康状态" clearable style="width: 140px" @change="search">
            <el-option v-for="o in enums.group('clusterStatus')" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <el-select v-model="filters.deployMode" placeholder="部署模式" clearable style="width: 150px" @change="search">
            <el-option v-for="o in enums.group('deployMode')" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <el-select v-model="filters.arch" placeholder="CPU 架构" clearable style="width: 140px" @change="search">
            <el-option v-for="o in enums.group('cpuArch')" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <el-button @click="search">查询</el-button>
          <el-button text @click="resetFilters">重置</el-button>
        </div>
        <div class="toolbar-right text-muted">共 {{ total }} 个集群</div>
      </div>

      <!-- 错误态：展示原因 + 重试，避免出现「空白表格」无法判断是空数据还是请求失败 -->
      <el-alert
        v-if="errorMessage"
        type="error"
        show-icon
        :closable="false"
        class="mb-12"
        :title="`列表加载失败：${errorMessage}`"
      >
        <el-button size="small" @click="load">重试</el-button>
      </el-alert>

      <el-table
        v-loading="loading"
        :data="rows"
        row-key="id"
        empty-text="暂无纳管集群，可点击右上角「接管集群」开始"
        @selection-change="onSelectionChange"
      >
        <el-table-column type="selection" width="44" :selectable="(row: ClusterBrief) => !row.draft" />
        <el-table-column label="集群" min-width="200">
          <template #default="{ row }">
            <div class="cluster-cell">
              <router-link class="cluster-name" :to="`/clusters/${row.id}`">{{ row.name }}</router-link>
              <el-tag v-if="row.demo" size="small" type="danger" effect="dark">演示数据</el-tag>
              <el-tag v-if="row.draft" size="small" type="info" effect="plain">草稿</el-tag>
              <el-tag v-if="row.collecting" size="small" type="warning" effect="plain">采集中</el-tag>
            </div>
          </template>
        </el-table-column>
        <!-- 中控机独立成列：不同中控机上允许存在同名集群，这一列是区分它们的唯一依据 -->
        <el-table-column label="中控机" min-width="170">
          <template #default="{ row }">
            <span class="mono text-muted">{{ row.tiupHost ? `${row.tiupHost}:${row.tiupPort}` : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="健康状态" width="180">
          <template #default="{ row }">
            <div class="status-cell">
              <StatusTag :status="row.status" />
              <!-- 实例异常徽标：点击直达实例页并按「非 Up」筛选 -->
              <router-link
                v-if="row.abnormalInstanceCount > 0"
                :to="{ path: `/clusters/${row.id}/instances`, query: { status: 'abnormal' } }"
                class="no-underline"
              >
                <el-tag type="danger" size="small" effect="plain">实例异常 {{ row.abnormalInstanceCount }}</el-tag>
              </router-link>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="采集状态" width="150">
          <template #default="{ row }">
            <StatusTag :status="row.collectStatus" kind="collect" />
            <el-tooltip v-if="row.lastCollectError" :content="row.lastCollectError" placement="top">
              <el-icon class="err-icon"><WarningFilled /></el-icon>
            </el-tooltip>
            <!-- 告警徽标：数量 + 按最高级别着色（MVP 未接入告警时为 0，不展示） -->
            <el-tooltip
              v-if="row.alertCount > 0"
              :content="`活跃告警 ${row.alertCount} 条`"
              placement="top"
            >
              <el-tag :type="alertTagType(row.alertMaxLevel)" size="small" effect="dark" class="ml-6">
                {{ row.alertCount }}
              </el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column prop="tidbVersion" label="TiDB 版本" width="110">
          <template #default="{ row }">{{ row.tidbVersion || '—' }}</template>
        </el-table-column>
        <el-table-column label="部署模式" width="120">
          <template #default="{ row }">{{ enums.label('deployMode', row.deployMode) }}</template>
        </el-table-column>
        <el-table-column prop="cpuArch" label="架构" width="90" />
        <el-table-column label="规模" width="150">
          <template #default="{ row }">
            <span class="text-muted">
              {{ row.instanceCount }} 实例 / {{ row.hostCount }} 主机
            </span>
          </template>
        </el-table-column>
        <el-table-column label="最近采集" width="160">
          <template #default="{ row }">{{ formatTime(row.lastCollectAt, 'MM-DD HH:mm:ss') }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="160">
          <template #default="{ row }">{{ formatTime(row.createdAt, 'YYYY-MM-DD HH:mm') }}</template>
        </el-table-column>
        <el-table-column label="操作" width="190" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="router.push(`/clusters/${row.id}`)">详情</el-button>
            <el-button
              v-if="auth.can('cluster.refresh')"
              link
              type="primary"
              :disabled="row.draft || row.collecting"
              @click="refreshOne(row)"
            >
              采集
            </el-button>
            <el-button
              v-if="auth.can('cluster.edit')"
              link
              type="primary"
              @click="openEdit(row)"
            >
              编辑
            </el-button>
            <el-button v-if="auth.can('cluster.delete')" link type="danger" @click="openRemove(row)">移除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        class="mt-12"
        layout="total, sizes, prev, pager, next"
        :total="total"
        :page-size="filters.pageSize"
        :current-page="filters.page"
        :page-sizes="[10, 20, 50]"
        @size-change="onSizeChange"
        @current-change="onPageChange"
      />
    </el-card>

    <!-- 接管 / 编辑 -->
    <el-dialog
      v-model="dialogVisible"
      :title="editing ? '编辑纳管信息' : '接管集群'"
      width="640px"
      :close-on-click-modal="false"
      @closed="resetForm"
    >
      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="mb-12"
        title="接管只读取集群信息，不会修改被纳管集群；凭据加密存储，接口仅返回掩码。"
      />
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item label="集群名称" prop="name">
          <el-input v-model="form.name" placeholder="字母开头，3~40 位字母/数字/下划线/短横线，如 prod-tidb" :disabled="editing" />
          <span class="text-muted form-hint">同一中控机内集群名不可重复（不同中控机可使用相同名称）</span>
        </el-form-item>
        <el-divider content-position="left">OS 通道（TiUP 元数据）</el-divider>
        <el-row :gutter="12">
          <el-col :span="16">
            <el-form-item label="TiUP 主机" prop="tiupHost">
              <el-input v-model="form.tiupHost" placeholder="10.0.0.11" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="端口" prop="tiupPort" label-width="60px">
              <el-input-number v-model="form.tiupPort" :min="1" :max="65535" controls-position="right" style="width: 100%" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="SSH 用户" prop="tiupUser">
              <el-input v-model="form.tiupUser" placeholder="tidb" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="SSH 密码">
              <el-input
                v-model="form.sshPassword"
                type="password"
                show-password
                :placeholder="editing ? '留空表示不修改' : 'SSH 密码'"
              />
            </el-form-item>
          </el-col>
        </el-row>
        <el-divider content-position="left">数据库通道（MySQL 协议）</el-divider>
        <el-row :gutter="12">
          <el-col :span="16">
            <el-form-item label="TiDB 主机" prop="dbHost">
              <el-input v-model="form.dbHost" placeholder="10.0.0.11" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="端口" label-width="60px" prop="dbPort">
              <el-input-number v-model="form.dbPort" :min="1" :max="65535" controls-position="right" style="width: 100%" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="数据库用户" prop="dbUser">
              <el-input v-model="form.dbUser" placeholder="root" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="数据库密码" prop="dbPassword">
              <el-input
                v-model="form.dbPassword"
                type="password"
                show-password
                :placeholder="editing ? '留空表示不修改' : '数据库密码'"
              />
            </el-form-item>
          </el-col>
        </el-row>
        <el-divider content-position="left">PD 通道（可选，留空自动识别）</el-divider>
        <el-form-item label="PD 地址" prop="pdEndpoint">
          <div style="width: 100%">
            <el-input
              v-model="form.pdEndpoint"
              placeholder="留空自动识别，也可手填 http://10.0.0.11:2379"
            >
              <template #append>
                <el-button :loading="discovering" @click="autoDiscoverPd">自动获取</el-button>
              </template>
            </el-input>
            <div class="text-muted" style="margin-top: 4px; line-height: 1.6">
              留空时接管会自动从 TiUP 元数据（meta.yaml 的 pd_servers）识别 PD 节点，读不到则回退到
              information_schema.cluster_info；PD 为可选通道，识别失败不影响纳管。
            </div>
          </div>
        </el-form-item>
        <el-form-item v-if="!editing" label="仅存草稿">
          <el-switch v-model="form.saveAsDraft" />
          <span class="text-muted" style="margin-left: 8px">草稿不参与统计与巡检，无需连通性测试</span>
        </el-form-item>
        <el-form-item v-if="!editing && isAdmin" label="跳过校验">
          <el-switch v-model="forceSkip" />
          <span class="text-muted" style="margin-left: 8px">
            强制接管（不校验连接测试结果，审计记录 force=true）
          </span>
        </el-form-item>
      </el-form>

      <el-alert
        v-if="discoveredPd && !form.pdEndpoint && discoveredPd.endpoints.length > 0"
        type="success"
        :closable="false"
        show-icon
        class="mt-12"
        :title="`已自动识别 PD 地址：${discoveredPd.endpoints.join('、')}`"
        :description="`${discoveredPd.message}，接管后写入集群信息，无需手动填写。`"
      />
      <el-alert
        v-if="demoNotice"
        type="error"
        :closable="true"
        show-icon
        class="mt-12"
        title="演示模式：以下结果不是真实集群"
        :description="demoNotice"
        @close="demoNotice = ''"
      />
      <div v-if="probeResults.length > 0">
        <el-divider content-position="left">连通性测试结果</el-divider>
        <ProbeResultList :results="probeResults" />
      </div>
      <el-alert
        v-if="!editing && !form.saveAsDraft && !testToken && !forceSkip"
        type="warning"
        :closable="false"
        show-icon
        class="mt-12"
        title="提交前必须先「测试连通性」：服务端用测试凭证校验本次连接参数未被篡改"
      />

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button :loading="testing" @click="testConnection">测试连通性</el-button>
        <el-button
          type="primary"
          :loading="submitting"
          :disabled="submitDisabled"
          @click="submitForm"
        >
          {{ editing ? '保存修改' : '开始接管' }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 移除纳管 -->
    <el-dialog v-model="removeVisible" title="移除纳管" width="440px">
      <p>
        将移除集群 <strong>{{ removing?.name }}</strong> 的纳管信息与本地快照数据。
        <br />
        <span class="text-muted">此操作不影响真实集群，可再次接管恢复。</span>
      </p>
      <el-form label-width="90px">
        <el-form-item label="确认名称">
          <el-input v-model="confirmName" :placeholder="`请输入 ${removing?.name}`" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="removeVisible = false">取消</el-button>
        <el-button
          type="danger"
          :disabled="confirmName !== removing?.name"
          :loading="removingJob"
          @click="confirmRemove"
        >
          确认移除
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { Plus, Refresh, Search, WarningFilled } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import ProbeResultList from '@/components/ProbeResultList.vue'
import { clusterApi, type PDDiscovery, type TakeoverForm } from '@/api'
import type { ClusterBrief, ProbeResult } from '@/api/types'
import { formatTime } from '@/utils/format'
import { queryInt, queryString, syncQuery } from '@/utils/query'
import { useAuthStore } from '@/stores/auth'
import { useEnumStore } from '@/stores/enums'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const enums = useEnumStore()

/** 提示过一次之后不再重复弹（避免 URL 同步导致组件重挂载时反复提示）。 */
let takeoverHinted = false

const loading = ref(false)
const rows = ref<ClusterBrief[]>([])
const total = ref(0)
const selection = ref<ClusterBrief[]>([])
const errorMessage = ref('')

const filters = reactive({
  keyword: '',
  status: '',
  deployMode: '',
  arch: '',
  page: 1,
  pageSize: 20,
})

const dialogVisible = ref(false)
const editing = ref<ClusterBrief | null>(null)
const testing = ref(false)
const submitting = ref(false)
const discovering = ref(false)
const probeResults = ref<ProbeResult[]>([])
// 演示模式下后端返回的提示语（明确「未连接真实集群」），在探测结果上方以红色告警展示。
const demoNotice = ref('')
const testToken = ref('')
/** 表单未填 PD 地址时，服务端自动发现的结果（仅用于提示，不自动回填以免与测试凭证指纹错位）。 */
const discoveredPd = ref<PDDiscovery | null>(null)
const forceSkip = ref(false)
const formRef = ref<FormInstance>()

const removeVisible = ref(false)
const removing = ref<ClusterBrief | null>(null)
const removingJob = ref(false)
const confirmName = ref('')

const isAdmin = computed(() => auth.user?.role === 'admin')

/** 提交按钮：编辑态 / 草稿态 / 已拿凭证 / admin 强制 四种情况之外一律置灰。 */
const submitDisabled = computed(() => {
  if (editing.value || form.saveAsDraft || forceSkip.value) {
    return false
  }
  return !testToken.value
})

const emptyForm = (): TakeoverForm => ({
  name: '',
  tiupHost: '',
  tiupPort: 22,
  tiupUser: 'tidb',
  sshPassword: '',
  dbHost: '',
  dbPort: 4000,
  dbUser: 'root',
  dbPassword: '',
  pdEndpoint: '',
  saveAsDraft: false,
})

const form = reactive<TakeoverForm>(emptyForm())

const rules: FormRules = {
  name: [
    { required: true, message: '请输入集群名称', trigger: 'blur' },
    {
      pattern: /^[A-Za-z][A-Za-z0-9_-]{2,39}$/,
      message: '以字母开头，3~40 位字母、数字、下划线或短横线',
      trigger: 'blur',
    },
  ],
  tiupHost: [{ required: true, message: '请输入 TiUP 主机', trigger: 'blur' }],
  tiupUser: [{ required: true, message: '请输入 SSH 用户', trigger: 'blur' }],
  dbHost: [{ required: true, message: '请输入 TiDB 主机', trigger: 'blur' }],
  dbUser: [{ required: true, message: '请输入数据库用户', trigger: 'blur' }],
  dbPassword: [
    {
      validator: (_r, value: string, cb) => {
        if (!editing.value && !value) {
          cb(new Error('请输入数据库密码'))
          return
        }
        cb()
      },
      trigger: 'blur',
    },
  ],
  // PD 地址可选：留空由服务端从 TiUP 元数据 / cluster_info 自动识别，手填时才校验格式。
  pdEndpoint: [
    {
      validator: (_r, value: string, cb) => {
        const v = (value || '').trim()
        if (!v) {
          cb()
          return
        }
        if (!/^(https?:\/\/)?[^\s/:]+(:\d{1,5})$/.test(v)) {
          cb(new Error('格式应为 host:port 或 http(s)://host:port'))
          return
        }
        cb()
      },
      trigger: 'blur',
    },
  ],
}

/** 把筛选与分页写入 URL query（刷新 / 分享 / 前进后退保持状态）。 */
function syncUrl(): void {
  syncQuery(router, route.query, {
    keyword: filters.keyword,
    status: filters.status,
    deployMode: filters.deployMode,
    arch: filters.arch,
    page: filters.page > 1 ? filters.page : undefined,
    pageSize: filters.pageSize !== 20 ? filters.pageSize : undefined,
  })
}

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await clusterApi.list({
      page: filters.page,
      pageSize: filters.pageSize,
      keyword: filters.keyword || undefined,
      status: filters.status || undefined,
      deployMode: filters.deployMode || undefined,
      arch: filters.arch || undefined,
    })
    rows.value = res.list || []
    total.value = res.total || 0
  } catch (err) {
    rows.value = []
    total.value = 0
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

function resetFilters(): void {
  filters.keyword = ''
  filters.status = ''
  filters.deployMode = ''
  filters.arch = ''
  search()
}

function onSelectionChange(val: ClusterBrief[]): void {
  selection.value = val
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

/** 告警徽标颜色：按最高告警级别（critical 红 / warning 橙 / 其他灰蓝）。 */
function alertTagType(level?: string): 'danger' | 'warning' | 'info' {
  if (level === 'critical') {
    return 'danger'
  }
  if (level === 'warning') {
    return 'warning'
  }
  return 'info'
}

function openTakeover(): void {
  editing.value = null
  Object.assign(form, emptyForm())
  probeResults.value = []
  testToken.value = ''
  discoveredPd.value = null
  forceSkip.value = false
  dialogVisible.value = true
}

function openEdit(row: ClusterBrief): void {
  editing.value = row
  Object.assign(form, {
    ...emptyForm(),
    name: row.name,
    tiupHost: row.tiupHost,
    tiupPort: row.tiupPort,
    tiupUser: row.tiupUser,
    dbHost: row.tiupHost,
    dbUser: '',
    dbPassword: '',
    sshPassword: '',
    pdEndpoint: '',
  })
  probeResults.value = []
  testToken.value = ''
  discoveredPd.value = null
  forceSkip.value = false
  dialogVisible.value = true
  // 编辑态补齐纳管详情（PD 地址、DB 地址等不在列表字段内）。
  void clusterApi.get(row.id).then((detail) => {
    Object.assign(form, {
      dbHost: detail.dbHost,
      dbPort: detail.dbPort,
      dbUser: detail.dbUser,
      pdEndpoint: detail.pdEndpoint,
    })
  })
}

function resetForm(): void {
  probeResults.value = []
  testToken.value = ''
  forceSkip.value = false
  formRef.value?.clearValidate()
}

/** 自动获取 PD 地址：无需用户记忆 PD 节点，交给平台从 TiUP 元数据识别。 */
async function autoDiscoverPd(): Promise<void> {
  if (!form.name) {
    ElMessage.warning('请先填写集群名称：TiUP 元数据按集群名定位')
    return
  }
  if (!form.tiupHost) {
    ElMessage.warning('请先填写 TiUP 主机')
    return
  }
  discovering.value = true
  try {
    const res = await clusterApi.discoverPD({ ...form })
    if (!res.endpoints || res.endpoints.length === 0) {
      ElMessage.warning('未识别到 PD 地址，请手动填写')
      return
    }
    form.pdEndpoint = res.primary || res.endpoints[0]
    ElMessage.success(`已获取 PD 地址：${form.pdEndpoint}（${res.message || res.source}）`)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '自动获取 PD 地址失败，可手动填写')
  } finally {
    discovering.value = false
  }
}

async function testConnection(): Promise<void> {
  testing.value = true
  probeResults.value = []
  discoveredPd.value = null
  try {
    const res = await clusterApi.testConnection({ ...form })
    probeResults.value = res.results || []
    testToken.value = res.testToken || ''
    discoveredPd.value = res.discovered || null
    demoNotice.value = res.notice || ''
    const okCount = probeResults.value.filter((r) => r.ok).length
    if (okCount === probeResults.value.length && probeResults.value.length > 0) {
      ElMessage.success(
        discoveredPd.value ? '三通道全部连通，PD 地址已自动识别，可提交接管' : '三通道全部连通，可提交接管',
      )
    } else if (okCount > 0) {
      ElMessage.warning(`${okCount}/${probeResults.value.length} 通道连通，请检查失败通道`)
    } else {
      ElMessage.error('三通道均不可用，请检查地址与凭据')
    }
  } finally {
    testing.value = false
  }
}

// 连接参数一旦变化，旧凭证立即失效（服务端会按新参数重算指纹），本地同步清空。
watch(
  () => [form.tiupHost, form.tiupUser, form.tiupPort, form.dbHost, form.dbPort, form.pdEndpoint],
  () => {
    if (testToken.value) {
      testToken.value = ''
      probeResults.value = []
    }
  },
)

async function submitForm(): Promise<void> {
  if (!formRef.value) {
    return
  }
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) {
    return
  }
  submitting.value = true
  try {
    if (editing.value) {
      await clusterApi.update(editing.value.id, {
        tiupHost: form.tiupHost,
        tiupPort: form.tiupPort,
        tiupUser: form.tiupUser,
        sshPassword: form.sshPassword || undefined,
        dbHost: form.dbHost,
        dbPort: form.dbPort,
        dbUser: form.dbUser,
        dbPassword: form.dbPassword || undefined,
        pdEndpoint: form.pdEndpoint,
      })
      ElMessage.success('纳管信息已更新，建议立即刷新采集')
      dialogVisible.value = false
      await load()
      return
    }
    const res = await clusterApi.create({
      ...form,
      testToken: testToken.value || undefined,
      force: forceSkip.value || undefined,
    })
    if (res.demo) {
      ElMessage.warning(res.notice || '演示模式：该集群信息来自内置样例，不代表真实集群')
    } else {
      ElMessage.success(res.forced ? '已按管理员强制接管，正在采集集群信息' : '接管任务已提交，正在采集集群信息')
    }
    dialogVisible.value = false
    await load()
    if (res.jobId) {
      await router.push(`/jobs/${res.jobId}`)
    }
  } finally {
    submitting.value = false
  }
}

async function refreshOne(row: ClusterBrief): Promise<void> {
  const res = await clusterApi.refresh(row.id)
  ElMessage.success(res.reused ? `已有进行中任务 #${res.jobId}，已复用` : `采集任务 #${res.jobId} 已提交`)
  await load()
}

async function batchRefresh(): Promise<void> {
  const ids = selection.value.filter((row) => !row.draft).map((row) => row.id)
  if (ids.length === 0) {
    ElMessage.warning('请选择要采集的集群')
    return
  }
  const res = await clusterApi.batchRefresh(ids)
  ElMessage.success(`已提交 ${res.length} 个采集任务`)
  await load()
}

function openRemove(row: ClusterBrief): void {
  removing.value = row
  confirmName.value = ''
  removeVisible.value = true
}

async function confirmRemove(): Promise<void> {
  if (!removing.value) {
    return
  }
  removingJob.value = true
  try {
    await clusterApi.remove(removing.value.id, confirmName.value)
    ElMessage.success('已移除纳管')
    removeVisible.value = false
    await load()
  } finally {
    removingJob.value = false
  }
}

onMounted(async () => {
  await enums.ensure()
  // 从 URL 恢复筛选条件（刷新 / 分享链接 / 浏览器前进后退）。
  filters.keyword = queryString(route.query, 'keyword')
  filters.status = queryString(route.query, 'status')
  filters.deployMode = queryString(route.query, 'deployMode')
  filters.arch = queryString(route.query, 'arch')
  filters.page = queryInt(route.query, 'page', 1)
  filters.pageSize = queryInt(route.query, 'pageSize', 20)
  await load()
  // 首次进入若还没有集群，主动提示接管（避免空白困惑），每个页面会话只提示一次。
  if (!takeoverHinted && rows.value.length === 0 && !errorMessage.value && auth.can('cluster.takeover')) {
    takeoverHinted = true
    ElMessageBox.alert('当前还没有纳管任何集群，点击「接管集群」开始。', '提示', {
      confirmButtonText: '知道了',
    }).catch(() => false)
  }
})
</script>

<style scoped lang="scss">
.cluster-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.cluster-name {
  font-weight: 600;
  color: #2f6fed;
  text-decoration: none;
}

.status-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.no-underline {
  text-decoration: none;
}

.err-icon {
  margin-left: 6px;
  color: #e6a23c;
  vertical-align: middle;
}

.ml-6 {
  margin-left: 6px;
}

/* 表单内联提示（集群名唯一性口径） */
.form-hint {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.5;
}
</style>
