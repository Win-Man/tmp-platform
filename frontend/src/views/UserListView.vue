<template>
  <div class="page">
    <PageHeader title="平台用户" desc="管理登录账号与角色（admin / operator / viewer）">
      <template #actions>
        <el-button @click="matrixVisible = true">角色权限矩阵</el-button>
        <el-button v-if="auth.can('platform_user.manage')" type="primary" :icon="Plus" @click="openCreate">
          新建用户
        </el-button>
      </template>
    </PageHeader>

    <el-card shadow="never">
      <div class="table-toolbar">
        <div class="toolbar-left">
          <el-input
            v-model="keyword"
            placeholder="用户名 / 显示名"
            clearable
            style="width: 220px"
            :prefix-icon="Search"
            @keyup.enter="search"
            @clear="search"
          />
          <el-button @click="search">查询</el-button>
        </div>
        <div class="toolbar-right text-muted">共 {{ total }} 个账号</div>
      </div>

      <el-table v-loading="loading" :data="rows" size="small" empty-text="暂无用户">
        <el-table-column label="用户名" min-width="150">
          <template #default="{ row }">
            <span class="mono">{{ row.username }}</span>
            <el-tag v-if="row.id === auth.user?.id" size="small" type="success" effect="plain" style="margin-left: 6px">
              当前登录
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="displayName" label="显示名" min-width="140" />
        <el-table-column label="角色" width="120">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.role === 'admin' ? 'danger' : row.role === 'operator' ? 'warning' : 'info'">
              {{ enums.label('role', row.role) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <StatusTag :status="row.status === 'active' ? 'healthy' : 'error'" />
            <span style="margin-left: 4px">{{ row.status === 'active' ? '启用' : '禁用' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="首次改密" width="110">
          <template #default="{ row }">{{ row.mustChangePassword ? '待修改' : '已完成' }}</template>
        </el-table-column>
        <el-table-column label="最近登录" width="170">
          <template #default="{ row }">{{ formatTime(row.lastLoginAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="240" fixed="right">
          <template #default="{ row }">
            <el-button v-if="auth.can('platform_user.manage')" link type="primary" @click="openEdit(row)">
              编辑
            </el-button>
            <el-button
              v-if="auth.can('platform_user.manage')"
              link
              type="primary"
              @click="openReset(row)"
            >
              重置密码
            </el-button>
            <el-button
              v-if="auth.can('platform_user.manage')"
              link
              :type="row.status === 'active' ? 'danger' : 'success'"
              :disabled="row.id === auth.user?.id"
              @click="toggleStatus(row)"
            >
              {{ row.status === 'active' ? '禁用' : '启用' }}
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
    </el-card>

    <!-- 新建 / 编辑 -->
    <el-dialog v-model="dialogVisible" :title="editing ? '编辑用户' : '新建用户'" width="460px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" :disabled="Boolean(editing)" placeholder="登录名，字母数字下划线" />
        </el-form-item>
        <el-form-item label="显示名" prop="displayName">
          <el-input v-model="form.displayName" placeholder="如 张三" />
        </el-form-item>
        <el-form-item v-if="!editing" label="初始密码" prop="password">
          <el-input v-model="form.password" type="password" show-password />
          <div class="text-muted" style="font-size: 12px">至少 8 位，含字母和数字</div>
        </el-form-item>
        <el-form-item label="角色" prop="role">
          <el-select v-model="form.role" style="width: 100%">
            <el-option v-for="o in enums.group('role')" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码 -->
    <el-dialog v-model="resetVisible" title="重置密码" width="440px">
      <p class="text-muted">为用户 <strong>{{ editing?.username }}</strong> 设置新密码，重置后该用户所有会话立即失效。</p>
      <el-input v-model="newPassword" type="password" show-password placeholder="新密码（≥8 位，含字母数字）" />
      <template #footer>
        <el-button @click="resetVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitReset">确认重置</el-button>
      </template>
    </el-dialog>

    <!-- 权限矩阵 -->
    <el-drawer v-model="matrixVisible" title="角色权限矩阵" size="720px">
      <el-table :data="matrix" size="small" row-key="action" :span-method="spanMethod">
        <el-table-column prop="group" label="分组" width="100" />
        <el-table-column prop="name" label="操作" min-width="220" />
        <el-table-column label="admin" width="80" align="center">
          <template #default="{ row }"><el-icon v-if="row.admin" color="#67c23a"><Check /></el-icon></template>
        </el-table-column>
        <el-table-column label="operator" width="90" align="center">
          <template #default="{ row }"><el-icon v-if="row.operator" color="#67c23a"><Check /></el-icon></template>
        </el-table-column>
        <el-table-column label="viewer" width="80" align="center">
          <template #default="{ row }"><el-icon v-if="row.viewer" color="#67c23a"><Check /></el-icon></template>
        </el-table-column>
      </el-table>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { Check, Plus, Search } from '@element-plus/icons-vue'
import PageHeader from '@/components/PageHeader.vue'
import StatusTag from '@/components/StatusTag.vue'
import { userApi } from '@/api'
import type { RoleMatrixRow, UserProfile } from '@/api/types'
import { formatTime } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'
import { useEnumStore } from '@/stores/enums'

const auth = useAuthStore()
const enums = useEnumStore()

const loading = ref(false)
const rows = ref<UserProfile[]>([])
const total = ref(0)
const keyword = ref('')
const page = ref(1)
const pageSize = ref(20)

const dialogVisible = ref(false)
const editing = ref<UserProfile | null>(null)
const submitting = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({ username: '', displayName: '', password: '', role: 'viewer' })

const resetVisible = ref(false)
const newPassword = ref('')

const matrixVisible = ref(false)
const matrix = ref<RoleMatrixRow[]>([])

const rules: FormRules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9_]{3,32}$/, message: '3~32 位字母、数字或下划线', trigger: 'blur' },
  ],
  displayName: [{ required: true, message: '请输入显示名', trigger: 'blur' }],
  password: [
    {
      validator: (_r, value: string, cb) => {
        if (editing.value) {
          cb()
          return
        }
        if (!value || value.length < 8 || !/[A-Za-z]/.test(value) || !/\d/.test(value)) {
          cb(new Error('密码至少 8 位且包含字母和数字'))
          return
        }
        cb()
      },
      trigger: 'blur',
    },
  ],
  role: [{ required: true, message: '请选择角色', trigger: 'change' }],
}

const groups = computed(() => {
  const seen: string[] = []
  matrix.value.forEach((row) => {
    if (!seen.includes(row.group)) {
      seen.push(row.group)
    }
  })
  return seen
})

/** 合并「分组」列，便于阅读。 */
function spanMethod({ rowIndex, columnIndex }: { rowIndex: number; columnIndex: number }): number[] | undefined {
  if (columnIndex !== 0) {
    return undefined
  }
  const current = matrix.value[rowIndex]
  const prev = matrix.value[rowIndex - 1]
  if (prev && prev.group === current.group) {
    return [0, 0]
  }
  let span = 1
  for (let i = rowIndex + 1; i < matrix.value.length; i += 1) {
    if (matrix.value[i].group === current.group) {
      span += 1
    } else {
      break
    }
  }
  return [span, 1]
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await userApi.list({ page: page.value, pageSize: pageSize.value, keyword: keyword.value || undefined })
    rows.value = res.list || []
    total.value = res.total || 0
  } finally {
    loading.value = false
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
  editing.value = null
  Object.assign(form, { username: '', displayName: '', password: '', role: 'viewer' })
  dialogVisible.value = true
}

function openEdit(row: UserProfile): void {
  editing.value = row
  Object.assign(form, { username: row.username, displayName: row.displayName, password: '', role: row.role })
  dialogVisible.value = true
}

async function submit(): Promise<void> {
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
      await userApi.update(editing.value.id, { displayName: form.displayName, role: form.role })
      ElMessage.success('用户已更新')
    } else {
      await userApi.create({
        username: form.username,
        displayName: form.displayName,
        password: form.password,
        role: form.role,
      })
      ElMessage.success('用户已创建，该用户首次登录需修改密码')
    }
    dialogVisible.value = false
    await load()
  } finally {
    submitting.value = false
  }
}

function openReset(row: UserProfile): void {
  editing.value = row
  newPassword.value = ''
  resetVisible.value = true
}

async function submitReset(): Promise<void> {
  if (!editing.value) {
    return
  }
  if (newPassword.value.length < 8 || !/[A-Za-z]/.test(newPassword.value) || !/\d/.test(newPassword.value)) {
    ElMessage.warning('密码至少 8 位且包含字母和数字')
    return
  }
  submitting.value = true
  try {
    await userApi.resetPassword(editing.value.id, newPassword.value)
    ElMessage.success('密码已重置，该用户下次登录需先修改密码')
    resetVisible.value = false
  } finally {
    submitting.value = false
  }
}

async function toggleStatus(row: UserProfile): Promise<void> {
  const next = row.status === 'active' ? 'disabled' : 'active'
  const ok = await ElMessageBox.confirm(
    next === 'disabled' ? `禁用 ${row.username} 后其 token 立即失效，确认？` : `确认启用 ${row.username}？`,
    '修改状态',
    { type: 'warning' },
  ).catch(() => false)
  if (!ok) {
    return
  }
  await userApi.setStatus(row.id, next)
  ElMessage.success('状态已更新')
  await load()
}

async function loadMatrix(): Promise<void> {
  const res = await userApi.roleMatrix()
  matrix.value = res.items || []
}

onMounted(async () => {
  await enums.ensure()
  await load()
  await loadMatrix()
})
</script>
