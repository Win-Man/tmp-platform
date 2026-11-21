<template>
  <div>
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-input
          v-model="keyword"
          placeholder="参数名"
          clearable
          style="width: 200px"
          :prefix-icon="Search"
          @keyup.enter="search"
          @clear="search"
        />
        <el-checkbox v-model="onlyDiff" @change="search">仅看与默认值不同</el-checkbox>
        <el-button @click="search">查询</el-button>
      </div>
      <div class="toolbar-right">
        <el-button :loading="exporting" @click="exportCsv">导出 CSV</el-button>
      </div>
    </div>

    <el-alert
      v-if="errorMessage"
      type="error"
      show-icon
      :closable="false"
      class="mb-12"
      :title="`参数快照加载失败：${errorMessage}`"
    >
      <el-button size="small" @click="load">重试</el-button>
    </el-alert>

    <el-table v-loading="loading" :data="rows" size="small" empty-text="暂无参数快照">
      <el-table-column label="参数名" min-width="220">
        <template #default="{ row }">
          <span class="mono">{{ row.name }}</span>
          <el-tag v-if="row.differs" size="small" type="warning" effect="plain" style="margin-left: 6px">
            与默认不同
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="当前值" min-width="180">
        <template #default="{ row }">
          <span class="mono" :class="{ 'diff-value': row.differs }">{{ row.value || '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="默认值" min-width="150">
        <template #default="{ row }"><span class="mono text-muted">{{ row.defaultValue || '—' }}</span></template>
      </el-table-column>
      <el-table-column prop="scope" label="作用域" width="100" />
      <el-table-column label="动态" width="80">
        <template #default="{ row }">{{ row.isDynamic ? '是' : '否' }}</template>
      </el-table-column>
      <el-table-column prop="description" label="说明" min-width="200" show-overflow-tooltip />
      <el-table-column label="操作" width="100" fixed="right">
        <template #default="{ row }">
          <el-button
            v-if="auth.can('variable.update') && canEdit(row.name)"
            link
            type="primary"
            @click="openEdit(row)"
          >
            修改
          </el-button>
          <el-tooltip v-else-if="auth.can('variable.update')" content="该参数不在可修改白名单内">
            <span class="text-muted" style="font-size: 12px">不可改</span>
          </el-tooltip>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      class="mt-12"
      layout="total, sizes, prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      :page-sizes="[20, 50, 100]"
      @size-change="onSizeChange"
      @current-change="onPageChange"
    />

    <el-dialog v-model="editVisible" title="修改全局变量" width="520px" :close-on-click-modal="false">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        class="mb-12"
        title="仅白名单参数允许修改，修改立即在目标集群生效并记入审计。"
      />
      <el-form label-width="100px">
        <el-form-item label="参数名"><span class="mono">{{ editForm.name }}</span></el-form-item>
        <el-form-item label="当前值"><span class="mono">{{ editForm.oldValue || '—' }}</span></el-form-item>
        <el-form-item label="新值">
          <el-input v-model="editForm.value" placeholder="请输入新值" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitEdit">确认修改</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { downloadFile, tidbApi } from '@/api'
import type { VariableRow } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { useEnumStore } from '@/stores/enums'

const props = defineProps<{ clusterId: number }>()

const auth = useAuthStore()
const enums = useEnumStore()

const loading = ref(false)
const exporting = ref(false)
const rows = ref<VariableRow[]>([])
const total = ref(0)
const keyword = ref('')
const onlyDiff = ref(false)
const page = ref(1)
const pageSize = ref(20)
const errorMessage = ref('')

const editVisible = ref(false)
const submitting = ref(false)
const editForm = reactive({ name: '', value: '', oldValue: '' })

function canEdit(name: string): boolean {
  const whitelist = enums.dict?.variableWhitelist || []
  return whitelist.includes(name)
}

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await tidbApi.variables(props.clusterId, {
      page: page.value,
      pageSize: pageSize.value,
      keyword: keyword.value || undefined,
      onlyDiff: onlyDiff.value || undefined,
    })
    rows.value = res.list || []
    total.value = res.total || 0
  } catch (err) {
    // 失败时清空数据，避免把上一次的结果当成最新快照展示。
    rows.value = []
    total.value = 0
    errorMessage.value = err instanceof Error ? err.message : '请求失败'
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

function onSizeChange(size: number): void {
  pageSize.value = size
  page.value = 1
  void load()
}

async function exportCsv(): Promise<void> {
  exporting.value = true
  try {
    await downloadFile(tidbApi.exportVariablesUrl(props.clusterId, onlyDiff.value), 'variables.csv')
    ElMessage.success('导出已开始')
  } finally {
    exporting.value = false
  }
}

function openEdit(row: VariableRow): void {
  Object.assign(editForm, { name: row.name, value: row.value, oldValue: row.value })
  editVisible.value = true
}

async function submitEdit(): Promise<void> {
  submitting.value = true
  try {
    await tidbApi.updateVariable(props.clusterId, editForm.name, editForm.value)
    ElMessage.success(`参数 ${editForm.name} 已更新`)
    editVisible.value = false
    await load()
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  await enums.ensure()
  await load()
})
</script>

<style scoped lang="scss">
.diff-value {
  color: #e6a23c;
  font-weight: 600;
}
</style>
