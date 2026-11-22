<template>
  <el-container class="layout">
    <el-aside :width="app.sidebarCollapsed ? '64px' : '216px'" class="sidebar">
      <div class="brand">
        <span class="brand-logo">TMP</span>
        <span v-if="!app.sidebarCollapsed" class="brand-text">TiDB 管理平台</span>
      </div>
      <el-menu
        :default-active="activeMenu"
        :collapse="app.sidebarCollapsed"
        :collapse-transition="false"
        background-color="#1f2733"
        text-color="#c0c8d4"
        active-text-color="#ffffff"
        router
      >
        <el-menu-item index="/">
          <el-icon><Odometer /></el-icon>
          <template #title>总览</template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('cluster.view')" index="/clusters">
          <el-icon><Grid /></el-icon>
          <template #title>集群管理</template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('host.view')" index="/hosts">
          <el-icon><Monitor /></el-icon>
          <template #title>主机管理</template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('inspect.view')" index="/inspection">
          <el-icon><Aim /></el-icon>
          <template #title>巡检中心</template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('report.view')" index="/reports">
          <el-icon><Document /></el-icon>
          <template #title>巡检报告</template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('job.view')" index="/jobs">
          <el-icon><List /></el-icon>
          <template #title>
            <span>任务中心</span>
            <el-badge
              v-if="app.runningJobs > 0"
              :value="app.runningJobs"
              type="warning"
              class="menu-badge"
            />
          </template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('platform_user.view')" index="/users">
          <el-icon><UserFilled /></el-icon>
          <template #title>平台用户</template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('audit.view')" index="/audit">
          <el-icon><Tickets /></el-icon>
          <template #title>审计日志</template>
        </el-menu-item>
        <el-menu-item v-if="auth.can('setting.view')" index="/settings">
          <el-icon><Setting /></el-icon>
          <template #title>系统设置</template>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="topbar">
        <div class="topbar-left">
          <el-button text @click="app.toggleSidebar()">
            <el-icon><Fold /></el-icon>
          </el-button>
          <span class="topbar-title">{{ route.meta.title || '' }}</span>
        </div>
        <div class="topbar-right">
          <el-tooltip content="进行中任务" placement="bottom">
            <el-button text @click="goJobs">
              <el-badge :value="app.runningJobs" :hidden="app.runningJobs === 0" type="warning">
                <el-icon :class="{ spin: app.runningJobs > 0 }">
                  <Loading v-if="app.runningJobs > 0" />
                  <Bell v-else />
                </el-icon>
              </el-badge>
            </el-button>
          </el-tooltip>
          <el-dropdown trigger="click" @command="onCommand">
            <span class="user-entry">
              <el-avatar :size="28" class="avatar">{{ initial }}</el-avatar>
              <span class="user-name">{{ auth.displayName }}</span>
              <el-tag size="small" effect="plain">{{ roleLabel }}</el-tag>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="password">修改密码</el-dropdown-item>
                <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <!-- 演示模式常驻横幅：样例数据与真实集群数据必须一眼可辨（避免把内置数据当成线上信息） -->
      <el-alert
        v-if="mockMode"
        class="demo-banner"
        type="warning"
        :closable="false"
        show-icon
        title="演示模式：当前展示的是内置样例数据，未连接任何真实集群"
        :description="mockDescription"
      />

      <el-main class="main">
        <router-view v-slot="{ Component }">
          <component :is="Component" :key="route.fullPath" />
        </router-view>
      </el-main>
    </el-container>

    <ChangePasswordDialog v-model="passwordVisible" :force="auth.mustChangePassword" />
  </el-container>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { settingApi } from '@/api'
import { MUST_CHANGE_PASSWORD_EVENT } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import ChangePasswordDialog from '@/components/ChangePasswordDialog.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const app = useAppStore()

const passwordVisible = ref(false)
const mockMode = ref(false)
const mockForced = ref(false)
let mockTimer: number | null = null

/** 后端 40302（需先改初始密码）时重新强制弹出改密弹窗。 */
function onMustChangePassword(): void {
  passwordVisible.value = true
}

const activeMenu = computed(() => {
  if (route.path.startsWith('/clusters')) {
    return '/clusters'
  }
  if (route.path.startsWith('/reports')) {
    return '/reports'
  }
  if (route.path.startsWith('/jobs')) {
    return '/jobs'
  }
  return route.path
})

const initial = computed(() => (auth.displayName || 'U').slice(0, 1).toUpperCase())

const mockDescription = computed(() =>
  mockForced.value
    ? 'TMP_MOCK=true 已强制启用演示模式：接管、采集、巡检与报告结果均为模拟数据。要接真实集群，请去掉该环境变量后重启。'
    : '可在「系统设置 → 演示模式」关闭；关闭后接管真实集群才会走 SSH / MySQL / PD 真实采集。',
)

const roleLabels: Record<string, string> = { admin: '管理员', operator: 'DBA', viewer: '只读' }
const roleLabel = computed(() => roleLabels[auth.user?.role || ''] || auth.user?.role || '')

/** 拉取演示模式标记（非设置页也可感知当前数据源）。 */
async function refreshMockMode(): Promise<void> {
  try {
    const res = await settingApi.demoMode()
    mockMode.value = res.enabled
    mockForced.value = Boolean(res.forced)
  } catch {
    mockMode.value = false
  }
}

onMounted(async () => {
  app.startPolling()
  window.addEventListener(MUST_CHANGE_PASSWORD_EVENT, onMustChangePassword)
  if (auth.mustChangePassword) {
    passwordVisible.value = true
  }
  await refreshMockMode()
  mockTimer = window.setInterval(() => {
    void refreshMockMode()
  }, 60000)
})

onBeforeUnmount(() => {
  app.stopPolling()
  window.removeEventListener(MUST_CHANGE_PASSWORD_EVENT, onMustChangePassword)
  if (mockTimer !== null) {
    window.clearInterval(mockTimer)
    mockTimer = null
  }
})

watch(
  () => auth.mustChangePassword,
  (v) => {
    if (v) {
      passwordVisible.value = true
    }
  },
)

function goJobs(): void {
  void router.push('/jobs')
}

async function onCommand(command: string): Promise<void> {
  if (command === 'password') {
    passwordVisible.value = true
    return
  }
  if (command === 'logout') {
    await ElMessageBox.confirm('确认退出登录？', '提示', { type: 'warning' }).catch(() => false)
    await auth.logout()
    void router.replace('/login')
  }
}
</script>

<style scoped lang="scss">
.layout {
  height: 100vh;
}

// 演示模式横幅：贴住顶部、不参与滚动，确保任何页面都能看到「数据是样例」。
.demo-banner {
  border-radius: 0;
  border-left: none;
  border-right: none;
  flex-shrink: 0;
}

.sidebar {
  background: var(--tmp-sidebar-bg);
  transition: width 0.2s;
  overflow-x: hidden;

  .brand {
    height: 56px;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 16px;
    color: #fff;
    border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  }

  .brand-logo {
    font-weight: 700;
    letter-spacing: 1px;
    background: var(--tmp-sidebar-active);
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 13px;
  }

  .brand-text {
    font-size: 14px;
    white-space: nowrap;
  }

  :deep(.el-menu) {
    border-right: none;
  }

  :deep(.el-menu-item.is-active) {
    background: var(--tmp-sidebar-active) !important;
  }
}

.menu-badge {
  margin-left: 6px;
}

.topbar {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid var(--tmp-border);
  padding: 0 16px;
}

.topbar-left,
.topbar-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.topbar-title {
  font-size: 15px;
  font-weight: 600;
}

.user-entry {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  outline: none;
}

.avatar {
  background: #2f6fed;
  color: #fff;
  font-size: 13px;
}

.user-name {
  font-size: 13px;
}

.main {
  padding: 0;
  background: var(--tmp-bg);
  overflow-y: auto;
}

.spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}
</style>
