import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { authApi } from '@/api'
import { getToken, setToken } from '@/api/http'
import type { UserProfile } from '@/api/types'

/** 认证状态：token、当前用户与权限集合。 */
export const useAuthStore = defineStore('auth', () => {
  const token = ref<string>(getToken())
  const user = ref<UserProfile | null>(null)
  const permissions = ref<string[]>([])
  const loaded = ref(false)

  const isLoggedIn = computed(() => Boolean(token.value))
  const mustChangePassword = computed(() => Boolean(user.value?.mustChangePassword))
  const displayName = computed(() => user.value?.displayName || user.value?.username || '')

  /** 判断是否拥有某个操作权限（admin 由后端直接下发全部权限）。 */
  function can(action: string): boolean {
    if (!action) {
      return true
    }
    return permissions.value.includes(action)
  }

  /** 登录并加载权限。 */
  async function login(username: string, password: string): Promise<void> {
    const res = await authApi.login(username, password)
    token.value = res.token
    setToken(res.token)
    user.value = res.user
    await loadMe()
  }

  /** 拉取当前用户信息与权限集合。 */
  async function loadMe(): Promise<void> {
    const res = (await authApi.me()) as unknown as {
      user: UserProfile
      permissions: string[]
      role: string
    }
    user.value = res.user
    permissions.value = res.permissions || []
    loaded.value = true
  }

  /** 首次进入应用时确保用户信息已加载。 */
  async function ensureLoaded(): Promise<void> {
    if (!token.value || loaded.value) {
      return
    }
    await loadMe()
  }

  /** 退出登录（后端记录审计，前端清理本地状态）。 */
  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } catch {
      // 退出接口失败也要清理本地状态，避免卡在失效会话中。
    }
    token.value = ''
    user.value = null
    permissions.value = []
    loaded.value = false
    setToken('')
  }

  /** 本地清理（401 场景使用，避免再次请求后端）。 */
  function clear(): void {
    token.value = ''
    user.value = null
    permissions.value = []
    loaded.value = false
    setToken('')
  }

  /** 改密成功后同步 must_change_password 状态。 */
  function markPasswordChanged(): void {
    if (user.value) {
      user.value = { ...user.value, mustChangePassword: false }
    }
  }

  return {
    token,
    user,
    permissions,
    loaded,
    isLoggedIn,
    mustChangePassword,
    displayName,
    can,
    login,
    loadMe,
    ensureLoaded,
    logout,
    clear,
    markPasswordChanged,
  }
})
