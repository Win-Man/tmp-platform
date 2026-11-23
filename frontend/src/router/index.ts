import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { getToken, onUnauthorized } from '@/api/http'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { title: '登录', public: true },
  },
  {
    path: '/',
    component: () => import('@/layouts/MainLayout.vue'),
    children: [
      {
        path: '',
        name: 'overview',
        component: () => import('@/views/OverviewView.vue'),
        meta: { title: '总览', action: 'overview.view' },
      },
      {
        path: 'clusters',
        name: 'clusters',
        component: () => import('@/views/ClusterListView.vue'),
        meta: { title: '集群管理', action: 'cluster.view' },
      },
      {
        path: 'clusters/:id',
        component: () => import('@/views/ClusterDetailView.vue'),
        meta: { title: '集群详情', action: 'cluster.view' },
        // 集群详情为真正的子路由：切换 Tab 只换子组件，刷新/深链可直达任意子页。
        redirect: (to) => ({ name: 'cluster-overview', params: { id: to.params.id } }),
        children: [
          {
            path: 'overview',
            name: 'cluster-overview',
            component: () => import('@/views/cluster/ClusterOverviewTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '集群概览', action: 'cluster.view' },
          },
          {
            path: 'topology',
            name: 'cluster-topology',
            component: () => import('@/views/cluster/ClusterTopologyTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '集群拓扑', action: 'cluster.view' },
          },
          {
            path: 'instances',
            name: 'cluster-instances',
            component: () => import('@/views/cluster/ClusterInstancesTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '集群实例', action: 'cluster.view' },
          },
          {
            path: 'users',
            name: 'cluster-users',
            component: () => import('@/views/cluster/ClusterUsersTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: 'TiDB 用户', action: 'tidb_user.view' },
          },
          {
            path: 'resource-groups',
            name: 'cluster-resource-groups',
            component: () => import('@/views/cluster/ClusterResourceGroupsTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: 'Resource Group', action: 'resource_group.view' },
          },
          {
            path: 'sessions',
            name: 'cluster-sessions',
            component: () => import('@/views/cluster/ClusterSessionsTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '会话管理', action: 'session.view' },
          },
          {
            path: 'params',
            name: 'cluster-params',
            component: () => import('@/views/cluster/ClusterVariablesTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '参数管理', action: 'variable.view' },
          },
          {
            path: 'performance',
            name: 'cluster-performance',
            component: () => import('@/views/cluster/ClusterPerformanceTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '性能趋势', action: 'cluster.view' },
          },
          {
            path: 'inspection',
            name: 'cluster-inspection',
            component: () => import('@/views/cluster/ClusterInspectionTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '集群巡检', action: 'report.view' },
          },
          {
            path: 'addr-maps',
            name: 'cluster-addr-maps',
            component: () => import('@/views/cluster/ClusterAddrMapsTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '地址映射', action: 'cluster.view' },
          },
          {
            path: 'changes',
            name: 'cluster-changes',
            component: () => import('@/views/cluster/ClusterChangesTab.vue'),
            props: (route) => ({ clusterId: Number(route.params.id) }),
            meta: { title: '变更记录', action: 'cluster.view' },
          },
        ],
      },
      {
        path: 'hosts',
        name: 'hosts',
        component: () => import('@/views/HostListView.vue'),
        meta: { title: '主机管理', action: 'host.view' },
      },
      {
        path: 'inspection',
        name: 'inspection',
        component: () => import('@/views/InspectionView.vue'),
        meta: { title: '巡检中心', action: 'inspect.view' },
      },
      {
        path: 'reports',
        name: 'reports',
        component: () => import('@/views/ReportListView.vue'),
        meta: { title: '巡检报告', action: 'report.view' },
      },
      {
        path: 'reports/:id',
        name: 'report-detail',
        component: () => import('@/views/ReportDetailView.vue'),
        meta: { title: '报告详情', action: 'report.view' },
      },
      {
        path: 'jobs',
        name: 'jobs',
        component: () => import('@/views/JobListView.vue'),
        meta: { title: '任务中心', action: 'job.view' },
      },
      {
        path: 'jobs/:id',
        name: 'job-detail',
        component: () => import('@/views/JobDetailView.vue'),
        meta: { title: '任务详情', action: 'job.view' },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('@/views/UserListView.vue'),
        meta: { title: '平台用户', action: 'platform_user.view' },
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('@/views/AuditLogView.vue'),
        meta: { title: '审计日志', action: 'audit.view' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/SettingsView.vue'),
        meta: { title: '系统设置', action: 'setting.view' },
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/views/NotFoundView.vue'),
    meta: { title: '页面不存在', public: true },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})

// 路由守卫：未登录跳转登录页；无权限跳转总览。
router.beforeEach(async (to) => {
  const authed = Boolean(getToken())
  if (to.meta.public) {
    if (to.name === 'login' && authed) {
      return { name: 'overview' }
    }
    return true
  }
  if (!authed) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  const auth = useAuthStore()
  try {
    await auth.ensureLoaded()
  } catch {
    auth.clear()
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  const action = to.meta.action as string | undefined
  if (action && !auth.can(action)) {
    return { name: 'overview' }
  }
  return true
})

router.afterEach((to) => {
  const title = (to.meta.title as string) || ''
  document.title = title ? `${title} · TMP` : 'TMP · TiDB 管理平台'
})

// 401 时统一回到登录页（避免每个请求各自处理）。
onUnauthorized(() => {
  const auth = useAuthStore()
  auth.clear()
  const current = router.currentRoute.value
  if (current.name !== 'login') {
    void router.replace({ name: 'login', query: { redirect: current.fullPath } })
  }
})

export default router
