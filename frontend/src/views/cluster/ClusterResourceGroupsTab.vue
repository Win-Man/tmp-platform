<template>
  <div v-loading="loading">
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-button :icon="Refresh" :loading="loading" @click="refresh">刷新</el-button>
        <el-button v-if="auth.can('resource_group.manage')" type="primary" :icon="Plus" @click="openCreate">
          新建 Resource Group
        </el-button>
      </div>
      <div class="toolbar-right text-muted">
        共 {{ rows.length }} 个 Resource Group
        <el-tag v-if="live" size="small" effect="plain" type="success" style="margin-left: 8px">实时</el-tag>
        <el-tag v-else size="small" effect="plain" type="info" style="margin-left: 8px">采集快照</el-tag>
      </div>
    </div>

    <el-alert
      v-if="errorMessage"
      type="error"
      show-icon
      :closable="false"
      class="mb-12"
      :title="errorMessage"
    >
      <el-button size="small" @click="load">重试</el-button>
    </el-alert>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      class="mb-12"
      title="Resource Group 的绑定 / 解绑入口在「TiDB 用户」页：选择用户后点击「绑定 RG」。"
    />

    <el-table :data="rows" size="small" empty-text="暂无 Resource Group 快照（需 v7.5+ 且已完成采集）">
      <el-table-column label="名称" min-width="150">
        <template #default="{ row }">
          <span class="mono">{{ row.name }}</span>
          <el-tag v-if="row.name === 'default'" size="small" type="warning" effect="plain" style="margin-left: 6px">
            内置
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="RU/秒" width="120">
        <template #default="{ row }">{{ row.ruPerSec ?? '—' }}</template>
      </el-table-column>
      <el-table-column label="优先级" width="110">
        <template #default="{ row }">{{ row.priority ?? '—' }}</template>
      </el-table-column>
      <el-table-column label="Burstable" width="110">
        <template #default="{ row }">
          <el-tag :type="row.burstable ? 'success' : 'info'" size="small" effect="plain">
            {{ row.burstable ? '是' : '否' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="绑定用户数" width="110">
        <template #default="{ row }">{{ row.userCount ?? 0 }}</template>
      </el-table-column>
      <el-table-column label="采集时间" width="170">
        <template #default="{ row }">{{ formatTime(row.fetchedAt, 'MM-DD HH:mm:ss') }}</template>
      </el-table-column>
      <el-table-column v-if="auth.can('resource_group.manage')" label="操作" width="140" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button
            link
            type="danger"
            :disabled="row.name === 'default'"
            @click="removeRG(row)"
          >
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 新建 / 编辑 Resource Group -->
    <el-dialog
      v-model="dialogVisible"
      :title="editing ? `编辑 Resource Group：${form.name}` : '新建 Resource Group'"
      width="480px"
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" :disabled="editing" placeholder="字母、数字、下划线" />
        </el-form-item>
        <el-form-item label="RU/秒" prop="ruPerSec">
          <el-input v-model="form.ruPerSec" placeholder="非负整数，如 500" />
        </el-form-item>
        <el-form-item label="优先级">
          <el-select v-model="form.priority" clearable placeholder="默认 MEDIUM" style="width: 100%">
            <el-option label="LOW" value="LOW" />
            <el-option label="MEDIUM" value="MEDIUM" />
            <el-option label="HIGH" value="HIGH" />
          </el-select>
        </el-form-item>
        <el-form-item label="Burstable">
          <el-switch v-model="form.burstable" />
          <span class="text-muted" style="margin-left: 8px">允许超额占用资源</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitForm">
          {{ editing ? '保存' : '创建' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { tidbApi } from '@/api'
import type { ResourceGroupRow } from '@/api/types'
import { formatTime } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ clusterId: number }>()

const auth = useAuthStore()

const loading = ref(false)
const submitting = ref(false)
const live = ref(false)
const rows = ref<ResourceGroupRow[]>([])
const errorMessage = ref('')

const dialogVisible = ref(false)
const editing = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({ name: '', ruPerSec: '', priority: '', burstable: false })

const rules: FormRules = {
  name: [
    { required: true, message: '请输入名称', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9_]{1,64}$/, message: '仅允许字母、数字与下划线', trigger: 'blur' },
  ],
  ruPerSec: [
    { required: true, message: '请输入 RU_PER_SEC', trigger: 'blur' },
    { pattern: /^[0-9]+$/, message: '必须为非负整数', trigger: 'blur' },
  ],
}

// load(live=true) 实时查询 information_schema.resource_groups（刷新按钮 / CRUD 后），
// live=false 读取上次采集快照。实时查询失败时回退快照并提示。
async function load(useLive = false): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = useLive
      ? await tidbApi.refreshResourceGroups(props.clusterId, true)
      : await tidbApi.resourceGroups(props.clusterId)
    rows.value = res.list || []
    live.value = useLive
  } catch (err) {
    if (useLive) {
      // 实时查询失败（如版本不支持 / 连库失败），回退到快照，避免页面空白。
      try {
        const snap = await tidbApi.resourceGroups(props.clusterId)
        rows.value = snap.list || []
        live.value = false
      } catch {
        rows.value = []
      }
      errorMessage.value = err instanceof Error ? err.message : '实时刷新 Resource Group 失败'
    } else {
      rows.value = []
      errorMessage.value = err instanceof Error ? err.message : '加载 Resource Group 失败'
    }
  } finally {
    loading.value = false
  }
}

function refresh(): void {
  void load(true)
}

function openCreate(): void {
  editing.value = false
  Object.assign(form, { name: '', ruPerSec: '', priority: '', burstable: false })
  dialogVisible.value = true
}

function openEdit(row: ResourceGroupRow): void {
  editing.value = true
  Object.assign(form, {
    name: row.name,
    ruPerSec: row.ruPerSec ?? '',
    priority: row.priority ?? '',
    burstable: Boolean(row.burstable),
  })
  dialogVisible.value = true
}

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
    const payload = {
      ruPerSec: form.ruPerSec,
      priority: form.priority || undefined,
      burstable: form.burstable,
    }
    if (editing.value) {
      await tidbApi.updateRG(props.clusterId, form.name, payload)
      ElMessage.success(`Resource Group「${form.name}」已更新`)
    } else {
      await tidbApi.createRG(props.clusterId, { name: form.name, ...payload })
      ElMessage.success(`Resource Group「${form.name}」已创建`)
    }
    dialogVisible.value = false
    await load(true)
  } finally {
    submitting.value = false
  }
}

async function removeRG(row: ResourceGroupRow): Promise<void> {
  const ok = await ElMessageBox.confirm(
    `确认删除 Resource Group「${row.name}」？该操作不可撤销，且无法删除系统内置的 default。`,
    '删除 Resource Group',
    { type: 'warning' },
  ).catch(() => false)
  if (!ok) {
    return
  }
  try {
    await tidbApi.deleteRG(props.clusterId, row.name)
    ElMessage.success(`Resource Group「${row.name}」已删除`)
    await load(true)
  } catch (err) {
    // 错误已由全局拦截器提示，这里仅刷新兜底。
    void err
  }
}

onMounted(() => {
  void load()
})
</script>
