<template>
  <el-dialog
    v-model="visible"
    title="修改密码"
    width="480px"
    :close-on-click-modal="false"
    :close-on-press-escape="!force"
    :show-close="!force"
    :before-close="handleBeforeClose"
    @closed="resetForm"
  >
    <el-alert
      v-if="force"
      type="warning"
      :closable="false"
      show-icon
      title="首次登录必须修改初始密码后才能继续使用平台"
      class="mb-12"
    />
    <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
      <el-form-item label="当前密码" prop="oldPassword">
        <el-input v-model="form.oldPassword" type="password" show-password autocomplete="current-password" />
      </el-form-item>
      <el-form-item label="新密码" prop="newPassword">
        <el-input v-model="form.newPassword" type="password" show-password autocomplete="new-password" />
        <div class="text-muted" style="font-size: 12px">至少 8 位，需同时包含字母和数字</div>
      </el-form-item>
      <el-form-item label="确认新密码" prop="confirm">
        <el-input v-model="form.confirm" type="password" show-password autocomplete="new-password" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button v-if="!force" @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">确认修改</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { authApi } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { setToken } from '@/api/http'

const props = withDefaults(defineProps<{ modelValue: boolean; force?: boolean }>(), { force: false })
const emit = defineEmits<{ 'update:modelValue': [boolean]; success: [] }>()

const auth = useAuthStore()
const formRef = ref<FormInstance>()
const submitting = ref(false)

const visible = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const form = reactive({ oldPassword: '', newPassword: '', confirm: '' })

const rules: FormRules = {
  oldPassword: [{ required: true, message: '请输入当前密码', trigger: 'blur' }],
  newPassword: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    {
      validator: (_rule, value: string, callback) => {
        if (!value || value.length < 8) {
          callback(new Error('密码长度至少 8 位'))
          return
        }
        if (!/[A-Za-z]/.test(value) || !/\d/.test(value)) {
          callback(new Error('密码需同时包含字母和数字'))
          return
        }
        callback()
      },
      trigger: 'blur',
    },
  ],
  confirm: [
    { required: true, message: '请再次输入新密码', trigger: 'blur' },
    {
      validator: (_rule, value: string, callback) => {
        if (value !== form.newPassword) {
          callback(new Error('两次输入的密码不一致'))
          return
        }
        callback()
      },
      trigger: 'blur',
    },
  ],
}

function resetForm(): void {
  form.oldPassword = ''
  form.newPassword = ''
  form.confirm = ''
  formRef.value?.clearValidate()
}

/**
 * 关闭前拦截：强制改密模式下不允许关闭（无 "稍后"、"X"、ESC、点遮罩）。
 *
 * 这里是最后一道防线（同时已关闭 show-close / ESC / 点遮罩），
 * 确保 must_change_password=1 时用户无法绕过改密弹窗。
 */
function handleBeforeClose(done: () => void): void {
  if (props.force) {
    ElMessage.warning('请先修改初始密码')
    return
  }
  done()
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
    const res = await authApi.changePassword(form.oldPassword, form.newPassword)
    // 后端改密后 token_version+1，返回的新 token 需要立即替换，否则旧 token 已被吊销。
    if (res?.token) {
      setToken(res.token)
    }
    auth.markPasswordChanged()
    ElMessage.success('密码已修改')
    visible.value = false
    emit('success')
  } finally {
    submitting.value = false
  }
}
</script>
