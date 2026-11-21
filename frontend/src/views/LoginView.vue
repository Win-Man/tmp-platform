<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-head">
        <div class="logo">TMP</div>
        <div>
          <h1>TiDB 管理平台</h1>
          <p>集群纳管 · 状态巡检 · 对象运维</p>
        </div>
      </div>

      <el-form ref="formRef" :model="form" :rules="rules" size="large" @submit.prevent="submit">
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="用户名" autocomplete="username" clearable>
            <template #prefix><el-icon><User /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码"
            show-password
            autocomplete="current-password"
            @keyup.enter="submit"
          >
            <template #prefix><el-icon><Lock /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-button type="primary" class="submit" :loading="loading" @click="submit">登 录</el-button>
      </el-form>

      <el-alert
        v-if="errorMessage"
        :title="errorMessage"
        type="error"
        :closable="false"
        show-icon
        class="mt-12"
      />

      <p class="hint">
        首次部署默认账号 <code>admin / admin123</code>，登录后需立即修改密码。
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const formRef = ref<FormInstance>()
const loading = ref(false)
const errorMessage = ref('')
const form = reactive({ username: '', password: '' })

const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function submit(): Promise<void> {
  if (!formRef.value) {
    return
  }
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) {
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    await auth.login(form.username.trim(), form.password)
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || '/'
    await router.replace(redirect)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<style scoped lang="scss">
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: radial-gradient(circle at 20% 20%, #2b3a55 0%, #141a24 60%, #0d1219 100%);
  padding: 24px;
}

.login-card {
  width: 400px;
  background: #fff;
  border-radius: 12px;
  padding: 32px 32px 24px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.35);
}

.login-head {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-bottom: 24px;

  .logo {
    width: 48px;
    height: 48px;
    border-radius: 10px;
    background: #2f6fed;
    color: #fff;
    font-weight: 700;
    display: flex;
    align-items: center;
    justify-content: center;
    letter-spacing: 1px;
  }

  h1 {
    margin: 0;
    font-size: 18px;
  }

  p {
    margin: 4px 0 0;
    font-size: 12.5px;
    color: var(--tmp-text-muted);
  }
}

.submit {
  width: 100%;
}

.hint {
  margin: 16px 0 0;
  font-size: 12px;
  color: var(--tmp-text-muted);
  line-height: 1.6;

  code {
    background: #f2f4f8;
    padding: 1px 5px;
    border-radius: 3px;
  }
}
</style>
