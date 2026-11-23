<template>
  <div v-loading="loading">
    <div class="table-toolbar">
      <div class="toolbar-left">
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="canEdit" type="primary" :icon="Plus" @click="openCreate">新建映射</el-button>
        <el-button :icon="Aim" @click="openPreview">地址试算</el-button>
      </div>
      <div class="toolbar-right text-muted">共 {{ rows.length }} 条规则</div>
    </div>

    <el-alert type="info" :closable="false" show-icon class="mb-12">
      <template #title>
        用于多网络平面部署：集群按内网平面（如 10.10.9.x）登记地址，而 TMP 只能访问业务平面（如 158.1.9.x）时，
        在这里配置地址映射。映射只改写<strong>页面展示、跳转链接与 SSH / PD 连接目标</strong>，
        采集落库的原始地址不变，重新采集不会覆盖规则。
      </template>
    </el-alert>

    <el-alert v-if="errorMessage" type="error" show-icon :closable="false" class="mb-12" :title="errorMessage">
      <el-button size="small" @click="load">重试</el-button>
    </el-alert>

    <el-table :data="rows" size="small" empty-text="暂无地址映射规则">
      <el-table-column label="源地址（集群内网）" min-width="170">
        <template #default="{ row }"><span class="mono">{{ row.source }}</span></template>
      </el-table-column>
      <el-table-column label="" width="46" align="center">
        <template #default>→</template>
      </el-table-column>
      <el-table-column label="目标地址（TMP 可达）" min-width="170">
        <template #default="{ row }"><span class="mono">{{ row.target }}</span></template>
      </el-table-column>
      <el-table-column label="形态" width="90">
        <template #default="{ row }">
          <el-tag size="small" effect="plain" :type="row.source.includes('/') ? 'info' : 'warning'">
            {{ row.source.includes('/') ? '网段' : '单机' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="说明" min-width="140">
        <template #default="{ row }">{{ row.note || '—' }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-switch
            :model-value="row.enabled === 1"
            :disabled="!canEdit"
            @change="(v: string | number | boolean) => toggleEnabled(row, Boolean(v))"
          />
        </template>
      </el-table-column>
      <el-table-column label="更新时间" width="150">
        <template #default="{ row }">{{ formatTime(row.updatedAt, 'MM-DD HH:mm:ss') }}</template>
      </el-table-column>
      <el-table-column v-if="canEdit" label="操作" width="130" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 新建 / 编辑规则 -->
    <el-dialog
      v-model="dialogVisible"
      :title="editingId ? '编辑地址映射' : '新建地址映射'"
      width="560px"
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-form-item label="源地址" prop="source">
          <el-input v-model="form.source" placeholder="10.10.9.0/24 或 10.10.9. 或 10.10.9.11" />
          <div class="form-hint">集群里登记的地址：可填网段（/24 或 10.10.9.）、也可填单台 IP。</div>
        </el-form-item>
        <el-form-item label="目标地址" prop="target">
          <el-input v-model="form.target" placeholder="158.1.9.0/24 或 158.1.9. 或 158.1.9.21" />
          <div class="form-hint">TMP 能访问到的地址。形态需与源一致（网段对同长度网段，单 IP 对单 IP）。</div>
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="form.note" maxlength="200" placeholder="如：业务平面" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <el-form-item label="快捷填充">
          <div class="quick-fill">
            <el-button size="small" @click="fillExample('seg')">网段示例</el-button>
            <el-button size="small" @click="fillExample('host')">单机示例</el-button>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">
          {{ editingId ? '保存' : '创建' }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 试算：用现有启用规则验证某个地址会被映射成什么 -->
    <el-dialog v-model="previewVisible" title="地址试算" width="520px">
      <el-form label-width="72px" @submit.prevent>
        <el-form-item label="地址">
          <el-input
            v-model="previewInput"
            placeholder="10.10.9.11:2379 或 http://10.10.9.11:3000/d/xx"
            @keyup.enter="runPreview"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="previewVisible = false">关闭</el-button>
        <el-button type="primary" :loading="previewing" @click="runPreview">试算</el-button>
      </template>
      <el-alert
        v-if="previewResult"
        :type="previewResult.matched ? 'success' : 'warning'"
        :closable="false"
        show-icon
        class="mb-0"
      >
        <template #title>
          <span v-if="previewResult.matched">
            {{ previewResult.input }} → <span class="mono">{{ previewResult.mapped }}</span>
          </span>
          <span v-else>未命中任何启用规则，地址将保持原样：{{ previewResult.input }}</span>
        </template>
        <div v-if="previewResult.matched" class="text-muted">
          命中规则：{{ previewResult.source }} → {{ previewResult.target }}
        </div>
      </el-alert>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { Aim, Plus, Refresh } from '@element-plus/icons-vue'
import { clusterApi } from '@/api'
import type { AddrMapPreview, AddrMapRow } from '@/api/types'
import { formatTime } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ clusterId: number }>()

const auth = useAuthStore()
const canEdit = computed(() => auth.can('cluster.edit'))

const loading = ref(false)
const submitting = ref(false)
const rows = ref<AddrMapRow[]>([])
const errorMessage = ref('')

const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const formRef = ref<FormInstance>()
const form = reactive({ source: '', target: '', note: '', enabled: true })

const rules: FormRules = {
  source: [{ required: true, message: '请输入源地址', trigger: 'blur' }],
  target: [{ required: true, message: '请输入目标地址', trigger: 'blur' }],
}

const previewVisible = ref(false)
const previewing = ref(false)
const previewInput = ref('')
const previewResult = ref<AddrMapPreview | null>(null)

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await clusterApi.addrMaps(props.clusterId)
    rows.value = res.list || []
  } catch (err) {
    rows.value = []
    errorMessage.value = err instanceof Error ? err.message : '加载地址映射失败'
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editingId.value = null
  Object.assign(form, { source: '', target: '', note: '', enabled: true })
  dialogVisible.value = true
}

function openEdit(row: AddrMapRow): void {
  editingId.value = row.id
  Object.assign(form, {
    source: row.source,
    target: row.target,
    note: row.note ?? '',
    enabled: row.enabled === 1,
  })
  dialogVisible.value = true
}

function fillExample(kind: 'seg' | 'host'): void {
  if (kind === 'seg') {
    Object.assign(form, { source: '10.10.9.0/24', target: '158.1.9.0/24', note: '业务平面' })
  } else {
    Object.assign(form, { source: '10.10.9.11', target: '158.1.9.21', note: '' })
  }
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
    const payload = { source: form.source, target: form.target, note: form.note, enabled: form.enabled }
    if (editingId.value) {
      await clusterApi.updateAddrMap(props.clusterId, editingId.value, payload)
      ElMessage.success('地址映射已更新')
    } else {
      await clusterApi.createAddrMap(props.clusterId, payload)
      ElMessage.success('地址映射已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (err) {
    // 校验类错误（形态不一致、源地址重复）由全局拦截器提示，保留弹窗便于修正。
    void err
  } finally {
    submitting.value = false
  }
}

async function toggleEnabled(row: AddrMapRow, enabled: boolean): Promise<void> {
  try {
    await clusterApi.updateAddrMap(props.clusterId, row.id, {
      source: row.source,
      target: row.target,
      note: row.note ?? '',
      enabled,
    })
    row.enabled = enabled ? 1 : 0
    ElMessage.success(enabled ? '规则已启用' : '规则已停用')
  } catch (err) {
    // 失败时不改本地状态，避免界面与后端不一致。
    void err
  }
}

async function remove(row: AddrMapRow): Promise<void> {
  const ok = await ElMessageBox.confirm(
    `确认删除规则「${row.source} → ${row.target}」？删除后相关地址会恢复为原始内网地址。`,
    '删除地址映射',
    { type: 'warning' },
  ).catch(() => false)
  if (!ok) {
    return
  }
  try {
    await clusterApi.deleteAddrMap(props.clusterId, row.id)
    ElMessage.success('地址映射已删除')
    await load()
  } catch (err) {
    void err
  }
}

function openPreview(): void {
  previewResult.value = null
  previewVisible.value = true
}

async function runPreview(): Promise<void> {
  const addr = previewInput.value.trim()
  if (!addr) {
    ElMessage.warning('请输入要试算的地址')
    return
  }
  previewing.value = true
  try {
    previewResult.value = await clusterApi.previewAddrMap(props.clusterId, addr)
  } catch (err) {
    previewResult.value = null
    void err
  } finally {
    previewing.value = false
  }
}

onMounted(() => {
  void load()
})
</script>

<style scoped>
.form-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
  margin-top: 2px;
}

.quick-fill {
  display: flex;
  gap: 8px;
}

.mb-0 {
  margin-bottom: 0;
}
</style>
