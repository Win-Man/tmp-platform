import { defineStore } from 'pinia'
import { ref } from 'vue'
import { jobApi } from '@/api'

/** 应用级状态：顶栏进行中任务数（30s 轮询）。 */
export const useAppStore = defineStore('app', () => {
  const runningJobs = ref(0)
  const sidebarCollapsed = ref(false)
  let timer: number | null = null

  /** 刷新进行中任务数。 */
  async function refreshRunningJobs(): Promise<void> {
    try {
      const res = await jobApi.runningCount()
      runningJobs.value = res.count
    } catch {
      // 轮询失败静默处理，避免打扰用户。
    }
  }

  /** 启动轮询（仅登录后调用）。 */
  function startPolling(): void {
    if (timer !== null) {
      return
    }
    void refreshRunningJobs()
    timer = window.setInterval(() => {
      void refreshRunningJobs()
    }, 30000)
  }

  /** 停止轮询。 */
  function stopPolling(): void {
    if (timer !== null) {
      window.clearInterval(timer)
      timer = null
    }
  }

  /** 折叠/展开侧边栏。 */
  function toggleSidebar(): void {
    sidebarCollapsed.value = !sidebarCollapsed.value
  }

  return { runningJobs, sidebarCollapsed, refreshRunningJobs, startPolling, stopPolling, toggleSidebar }
})
