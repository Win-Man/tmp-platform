<template>
  <div>
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-input
          v-model="keyword"
          placeholder="用户名 / 主机"
          clearable
          style="width: 200px"
          :prefix-icon="Search"
          @keyup.enter="search"
          @clear="search"
        />
        <el-button @click="search">查询</el-button>
        <el-button v-if="auth.can('tidb_user.create')" type="primary" :icon="Plus" @click="openCreate">
          创建用户
        </el-button>
      </div>
      <div class="toolbar-right text-muted">
        共 {{ total }} 个用户
        <el-tag v-if="!supportsRG" size="small" effect="plain" type="info" style="margin-left: 8px">
          当前版本不支持 Resource Group
        </el-tag>
      </div>
    </div>

    <el-table v-loading="loading" :data="rows" size="small" empty-text="暂无 TiDB 用户快照">
      <el-table-column label="用户名" min-width="140">
        <template #default="{ row }">
          <span class="mono">{{ row.userName }}</span>
          <el-tag v-if="row.userName === 'root'" size="small" type="warning" effect="plain" style="margin-left: 6px">
            内置
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Host" width="150"><template #default="{ row }"><span class="mono">{{ row.host }}</span></template></el-table-column>
      <el-table-column prop="authPlugin" label="认证插件" width="180" />
      <el-table-column label="锁定" width="80">
        <template #default="{ row }">{{ row.accountLocked === 'Y' ? '是' : '否' }}</template>
      </el-table-column>
      <el-table-column label="Resource Group" width="150">
        <template #default="{ row }">
          <el-tag v-if="row.resourceGroup" size="small" effect="plain">{{ row.resourceGroup }}</el-tag>
          <span v-else class="text-muted">—</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="260" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openGrants(row)">权限</el-button>
          <el-button
            v-if="auth.can('tidb_user.password')"
            link
            type="primary"
            @click="openReset(row)"
          >
            重置密码
          </el-button>
          <el-button
            v-if="auth.can('resource_group.bind') && supportsRG"
            link
            type="primary"
            @click="openBind(row)"
          >
            绑定 RG
          </el-button>
          <el-button
            v-if="auth.can('tidb_user.delete')"
            link
            type="danger"
            :disabled="row.userName === 'root'"
            @click="removeUser(row)"
          >
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      class="mt-12"
      layout="total, prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      @current-change="onPageChange"
    />

    <h4 class="section-title mt-16">
      Resource Group
      <el-tag v-if="!supportsRG" size="small" type="info" effect="plain" style="margin-left: 8px">v7.5 起支持</el-tag>
    </h4>
    <el-table v-loading="rgLoading" :data="rgs" size="small" empty-text="暂无 Resource Group 数据">
      <el-table-column prop="name" label="名称" width="160" />
      <el-table-column prop="ruPerSec" label="RU/s" width="120" />
      <el-table-column prop="priority" label="优先级" width="120" />
      <el-table-column label="Burstable" width="110">
        <template #default="{ row }">{{ row.burstable ? '是' : '否' }}</template>
      </el-table-column>
      <el-table-column prop="userCount" label="绑定用户数" width="120" />
      <el-table-column prop="status" label="状态" />
    </el-table>

    <!-- 创建用户 -->
    <el-dialog v-model="createVisible" title="创建 TiDB 用户" width="520px" :close-on-click-modal="false">
      <el-form ref="createFormRef" :model="createForm" :rules="createRules" label-width="110px">
        <el-form-item label="用户名" prop="userName">
          <el-input v-model="createForm.userName" placeholder="如 app_readonly" />
        </el-form-item>
        <el-form-item label="允许来源" prop="host">
          <el-input v-model="createForm.host" placeholder="% 或 10.0.0.%" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input v-model="createForm.password" type="password" show-password />
        </el-form-item>
        <el-form-item v-if="supportsRG" label="Resource Group">
          <el-select v-model="createForm.resourceGroup" clearable placeholder="可选" style="width: 100%">
            <el-option v-for="rg in rgs" :key="rg.name" :label="rg.name" :value="rg.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="初始权限">
          <el-input v-model="createForm.grantsText" type="textarea" :rows="2" placeholder="可选，如 SELECT ON app.*" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitCreate">提交任务</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码 -->
    <el-dialog v-model="resetVisible" title="重置用户密码" width="460px">
      <p class="text-muted">将为 <strong class="mono">{{ current?.userName }}@{{ current?.host }}</strong> 重置密码，操作记入审计。</p>
      <el-input v-model="resetPassword" type="password" show-password placeholder="新密码" />
      <template #footer>
        <el-button @click="resetVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitReset">提交任务</el-button>
      </template>
    </el-dialog>

    <!-- 权限查看与变更 -->
    <el-dialog v-model="grantsVisible" title="用户权限" width="620px">
      <p class="text-muted">实时执行 SHOW GRANTS：<strong class="mono">{{ current?.userName }}@{{ current?.host }}</strong></p>
      <el-input :model-value="grants.join('\n')" type="textarea" :rows="6" readonly class="mono" />
      <template v-if="auth.can('tidb_user.grant')">
        <el-divider content-position="left">授予 / 回收</el-divider>
        <el-form label-width="90px">
          <el-form-item label="作用域">
            <el-radio-group v-model="grantForm.scope">
              <el-radio value="global">全局</el-radio>
              <el-radio value="db">库级</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item v-if="grantForm.scope === 'db'" label="数据库">
            <el-input v-model="grantForm.db" placeholder="如 app_db" />
          </el-form-item>
          <el-form-item label="权限">
            <el-input v-model="grantForm.privsText" placeholder="逗号分隔，如 SELECT,INSERT" />
          </el-form-item>
        </el-form>
      </template>
      <template #footer>
        <el-button @click="grantsVisible = false">关闭</el-button>
        <template v-if="auth.can('tidb_user.grant')">
          <el-button :loading="submitting" @click="submitGrants(true)">回收</el-button>
          <el-button type="primary" :loading="submitting" @click="submitGrants(false)">授予</el-button>
        </template>
      </template>
    </el-dialog>

    <!-- 绑定 Resource Group -->
    <el-dialog v-model="bindVisible" title="绑定 Resource Group" width="460px">
      <p class="text-muted">为 <strong class="mono">{{ current?.userName }}@{{ current?.host }}</strong> 绑定资源组</p>
      <el-select v-model="bindTarget" placeholder="选择资源组" style="width: 100%">
        <el-option v-for="rg in rgs" :key="rg.name" :label="rg.name" :value="rg.name" />
      </el-select>
      <template #footer>
        <el-button @click="bindVisible = false">取消</el-button>
        <el-button :loading="submitting" @click="submitBind(true)">解绑</el-button>
        <el-button type="primary" :loading="submitting" @click="submitBind(false)">绑定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessageBox, ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { Plus, Search } from '@element-plus/icons-vue'
import { clusterApi, tidbApi } from '@/api'
import type { Capabilities, ResourceGroupRow, TiDBUserRow } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ clusterId: number; caps?: Capabilities }>()

const auth = useAuthStore()

const loading = ref(false)
const rows = ref<TiDBUserRow[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')

const rgLoading = ref(false)
const rgs = ref<ResourceGroupRow[]>([])
const supportsRG = ref(Boolean(props.caps?.supportsResourceGroup))

// 父组件（集群详情）异步加载完能力信息后同步到本页，避免 RG 入口显隐滞后。
watch(
  () => props.caps,
  (caps) => {
    if (caps) {
      supportsRG.value = Boolean(caps.supportsResourceGroup)
    }
  },
)

const current = ref<TiDBUserRow | null>(null)
const submitting = ref(false)

const createVisible = ref(false)
const createFormRef = ref<FormInstance>()
const createForm = reactive({ userName: '', host: '%', password: '', resourceGroup: '', grantsText: '' })

const resetVisible = ref(false)
const resetPassword = ref('')

const grantsVisible = ref(false)
const grants = ref<string[]>([])
const grantForm = reactive({ scope: 'global', db: '', privsText: '' })

const bindVisible = ref(false)
const bindTarget = ref('')

const createRules: FormRules = {
  userName: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9_]{1,32}$/, message: '仅允许字母、数字与下划线', trigger: 'blur' },
  ],
  host: [{ required: true, message: '请输入允许来源', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await tidbApi.users(props.clusterId, {
      page: page.value,
      pageSize: pageSize.value,
      keyword: keyword.value || undefined,
    })
    rows.value = res.list || []
    total.value = res.total || 0
  } finally {
    loading.value = false
  }
}

async function loadRG(): Promise<void> {
  rgLoading.value = true
  try {
    const res = await tidbApi.resourceGroups(props.clusterId)
    rgs.value = res.list || []
    supportsRG.value = true
  } catch {
    supportsRG.value = false
    rgs.value = []
  } finally {
    rgLoading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function onPageChange(p: number): void {
  page.value = p
  void load()
}

function openCreate(): void {
  Object.assign(createForm, { userName: '', host: '%', password: '', resourceGroup: '', grantsText: '' })
  createVisible.value = true
}

async function submitCreate(): Promise<void> {
  if (!createFormRef.value) {
    return
  }
  const ok = await createFormRef.value.validate().catch(() => false)
  if (!ok) {
    return
  }
  submitting.value = true
  try {
    const grants = createForm.grantsText
      .split(/[,，]/)
      .map((g) => g.trim())
      .filter(Boolean)
    const res = await tidbApi.createUser(props.clusterId, {
      userName: createForm.userName,
      host: createForm.host,
      password: createForm.password,
      resourceGroup: supportsRG.value ? createForm.resourceGroup || undefined : undefined,
      grants,
    })
    ElMessage.success(`创建任务 #${res.jobId} 已提交`)
    createVisible.value = false
  } finally {
    submitting.value = false
  }
}

function openReset(row: TiDBUserRow): void {
  current.value = row
  resetPassword.value = ''
  resetVisible.value = true
}

async function submitReset(): Promise<void> {
  if (!current.value) {
    return
  }
  if (!resetPassword.value) {
    ElMessage.warning('请输入新密码')
    return
  }
  submitting.value = true
  try {
    const res = await tidbApi.resetPassword(props.clusterId, current.value.id, resetPassword.value)
    ElMessage.success(`重置任务 #${res.jobId} 已提交`)
    resetVisible.value = false
  } finally {
    submitting.value = false
  }
}

async function openGrants(row: TiDBUserRow): Promise<void> {
  current.value = row
  grants.value = []
  grantsVisible.value = true
  try {
    const res = await tidbApi.grants(props.clusterId, row.id)
    grants.value = res.grants || []
  } catch {
    grants.value = []
  }
}

async function submitGrants(revoke: boolean): Promise<void> {
  if (!current.value) {
    return
  }
  const privs = grantForm.privsText
    .split(/[,，]/)
    .map((p) => p.trim().toUpperCase())
    .filter(Boolean)
  if (privs.length === 0) {
    ElMessage.warning('请输入权限，如 SELECT,INSERT')
    return
  }
  submitting.value = true
  try {
    const res = await tidbApi.changeGrants(props.clusterId, current.value.id, {
      scope: grantForm.scope,
      db: grantForm.db || undefined,
      privs,
      revoke,
    })
    grants.value = res.grants || []
    ElMessage.success(revoke ? '权限已回收' : '权限已授予')
  } finally {
    submitting.value = false
  }
}

function openBind(row: TiDBUserRow): void {
  current.value = row
  bindTarget.value = row.resourceGroup || ''
  bindVisible.value = true
}

async function submitBind(unbind: boolean): Promise<void> {
  if (!current.value) {
    return
  }
  submitting.value = true
  try {
    if (unbind) {
      await tidbApi.unbindRG(props.clusterId, { userName: current.value.userName, host: current.value.host })
    } else {
      if (!bindTarget.value) {
        ElMessage.warning('请选择资源组')
        return
      }
      await tidbApi.bindRG(props.clusterId, {
        userName: current.value.userName,
        host: current.value.host,
        resourceGroup: bindTarget.value,
      })
    }
    ElMessage.success(unbind ? '已解绑 Resource Group' : '已绑定 Resource Group')
    bindVisible.value = false
    await load()
  } finally {
    submitting.value = false
  }
}

async function removeUser(row: TiDBUserRow): Promise<void> {
  const ok = await ElMessageBox.confirm(
    `确认删除用户 ${row.userName}@${row.host}？该操作不可撤销。`,
    '删除 TiDB 用户',
    { type: 'warning' },
  ).catch(() => false)
  if (!ok) {
    return
  }
  const res = await tidbApi.deleteUser(props.clusterId, row.id)
  ElMessage.success(`删除任务 #${res.jobId} 已提交`)
  await load()
}

onMounted(async () => {
  await load()
  await loadRG()
  // 能力信息缺失时主动补一次，避免低版本误展示 RG 操作。
  if (!props.caps) {
    try {
      const res = await clusterApi.capabilities(props.clusterId)
      supportsRG.value = Boolean(res.capabilities?.supportsResourceGroup)
    } catch {
      supportsRG.value = false
    }
  }
})
</script>

<style scoped lang="scss">
.section-title {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 8px;
}
</style>
