import { request, rawClient, type PageData } from './http'
import type {
  AboutInfo,
  AuditLogRow,
  Capabilities,
  ClusterBrief,
  ClusterOption,
  CollectStatusInfo,
  CollectionSettings,
  ComponentRow,
  CredentialView,
  DemoModeStatus,
  EnumDict,
  HealthItem,
  HostRow,
  InspectItem,
  InspectReport,
  InspectRun,
  InspectTemplate,
  InstanceRow,
  JobLog,
  JobRow,
  JobStep,
  LatestInspection,
  LoginResult,
  OverviewSummary,
  ProbeResult,
  ResourceGroupRow,
  RoleMatrixRow,
  SessionRow,
  SessionsPage,
  TiDBUserRow,
  TopologyGroup,
  TopologyResult,
  UserProfile,
  VariablesPage,
} from './types'

/** 集群详情（含纳管信息与凭据掩码）。 */
export interface ClusterDetail extends ClusterBrief {
  tiupDir: string
  dbHost: string
  dbPort: number
  dbUser: string
  pdEndpoint: string
  dashboardUrls?: string[]
  prometheusUrls?: string[]
  grafanaUrls?: string[]
  alertmanagerUrls?: string[]
  capabilities?: Capabilities
  credentials?: {
    hasSshPassword: boolean
    hasSshKey: boolean
    hasDbPassword: boolean
    updatedAt?: string | null
  }
}

/** 集群概览聚合。 */
export interface ClusterOverview {
  cluster: ClusterDetail
  components: { component: string; instanceCount: number; versions: string[]; upCount: number; downCount: number }[]
  versions: string[]
  versionConsistent: boolean
  instanceSummary: { component: string; up: number; down: number; total: number }[]
  recentJobs: JobRow[]
  fetchedAt?: string | null
  collectError?: string
  pdUnavailable: boolean
}

/** 主机列表响应（带 arch 可选值）。 */
export interface HostsPage extends PageData<HostRow> {
  arches: string[]
}

/** 巡检运行详情。 */
export interface InspectRunDetail {
  run: InspectRun
  template: InspectTemplate
  results: InspectResult[]
  categoryStats: { category: string; label: string; critical: number; warning: number; pass: number; na: number }[]
  reportId?: number | null
  reportNo?: string
  reportStatus?: string
}

export interface InspectResult {
  id?: number
  runId?: number
  itemCode: string
  itemName: string
  category: string
  categoryLabel?: string
  level: string
  actual: string
  expect: string
  suggestion: string
  detail?: string
  sortOrder?: number
}

/** 巡检报告详情。 */
export interface InspectReportDetail extends InspectReport {
  contentMd: string
  contentHtml: string
  results: InspectResult[]
  suggestions: { itemCode: string; itemName: string; level: string; suggestion: string }[]
  categoryStats: { category: string; label: string; critical: number; warning: number; pass: number; na: number }[]
}

/** 集群变更记录条目。 */
export interface ClusterChangeRow {
  id: string
  action: string
  username: string
  result: string
  detail: string
  jobId?: number
  createdAt: string
}

export interface TakeoverForm {
  name: string
  tiupHost: string
  tiupPort: number
  tiupUser: string
  sshPassword?: string
  sshKey?: string
  dbHost: string
  dbPort: number
  dbUser: string
  dbPassword: string
  pdEndpoint: string
  saveAsDraft?: boolean
  /** 连接测试凭证（由 test-connection 返回，10 分钟有效）。 */
  testToken?: string
  /** 仅 admin：跳过连接测试凭证校验（审计记录 force=true）。 */
  force?: boolean
}

/** PD 地址自动发现结果。 */
export interface PDDiscovery {
  endpoints: string[]
  primary?: string
  /** tiup_meta / cluster_info / mock */
  source: string
  message: string
  mock?: boolean
}

/** 连接测试结果：三项探测 + 后续接管所需凭证。 */
export interface TestConnectionResponse {
  results: ProbeResult[]
  testToken: string
  mock: boolean
  /** 演示模式：结果为内置样例，未连接真实集群。 */
  demo?: boolean
  /** 演示模式提示语（后端给出，直接展示给用户）。 */
  notice?: string
  /** 表单未填 PD 地址时，服务端自动发现的结果（发现失败时为 null）。 */
  discovered?: PDDiscovery | null
}

export interface RefreshResult {
  clusterId: number
  jobId: number
  reused?: boolean
  /** 批量提交时该集群的失败原因；jobId 为 0 表示未提交成功。 */
  error?: string
}

/** 批量采集返回体：服务端包装成 { results: [...] }，不是裸数组。 */
export interface BatchRefreshResult {
  results: RefreshResult[]
}

// ---------------------------------------------------------------------------
// 认证与用户
// ---------------------------------------------------------------------------

export const authApi = {
  setupStatus: () => request<{ initialized: boolean }>({ url: '/setup/status', method: 'get' }),
  login: (username: string, password: string) =>
    request<LoginResult>({ url: '/auth/login', method: 'post', data: { username, password } }),
  logout: () => request<null>({ url: '/auth/logout', method: 'post' }),
  me: () => request<UserProfile>({ url: '/auth/me', method: 'get' }),
  changePassword: (oldPassword: string, newPassword: string) =>
    request<{ token?: string }>({ url: '/auth/password', method: 'put', data: { oldPassword, newPassword } }),
}

export const userApi = {
  list: (params: { page?: number; pageSize?: number; keyword?: string }) =>
    request<PageData<UserProfile>>({ url: '/users', method: 'get', params }),
  create: (data: { username: string; displayName: string; password: string; role: string }) =>
    request<UserProfile>({ url: '/users', method: 'post', data }),
  update: (id: number, data: { displayName: string; role: string }) =>
    request<UserProfile>({ url: `/users/${id}`, method: 'put', data }),
  setStatus: (id: number, status: string) =>
    request<null>({ url: `/users/${id}/status`, method: 'patch', data: { status } }),
  resetPassword: (id: number, password: string) =>
    request<null>({ url: `/users/${id}/reset-password`, method: 'post', data: { password } }),
  roleMatrix: () => request<{ items: RoleMatrixRow[] }>({ url: '/roles/matrix', method: 'get' }),
}

// ---------------------------------------------------------------------------
// 总览
// ---------------------------------------------------------------------------

export const overviewApi = {
  summary: () => request<OverviewSummary>({ url: '/overview/summary', method: 'get' }),
  health: () => request<HealthItem[]>({ url: '/overview/health-distribution', method: 'get' }),
  recentJobs: (limit = 10) =>
    request<JobRow[]>({ url: '/overview/recent-jobs', method: 'get', params: { limit } }),
  latestInspection: () => request<LatestInspection>({ url: '/overview/latest-inspection', method: 'get' }),
  collectStatus: () => request<CollectStatusInfo>({ url: '/overview/collect-status', method: 'get' }),
}

// ---------------------------------------------------------------------------
// 集群
// ---------------------------------------------------------------------------

export const clusterApi = {
  list: (params: Record<string, unknown>) =>
    request<PageData<ClusterBrief>>({ url: '/clusters', method: 'get', params }),
  options: () => request<ClusterOption[]>({ url: '/clusters/options', method: 'get' }),
  testConnection: (data: TakeoverForm) =>
    request<TestConnectionResponse>({
      url: '/clusters/test-connection',
      method: 'post',
      data,
      timeout: 30000,
    }),
  /** 自动获取 PD 地址（TiUP 元数据优先，其次 information_schema.cluster_info）。 */
  discoverPD: (data: Partial<TakeoverForm>) =>
    request<PDDiscovery>({ url: '/clusters/discover-pd', method: 'post', data, timeout: 20000 }),
  create: (data: TakeoverForm) =>
    request<{
      clusterId: number
      jobId: number
      forced?: boolean
      demo?: boolean
      notice?: string
    }>({ url: '/clusters', method: 'post', data }),
  get: (id: number) => request<ClusterDetail>({ url: `/clusters/${id}`, method: 'get' }),
  update: (id: number, data: Partial<TakeoverForm>) =>
    request<null>({ url: `/clusters/${id}`, method: 'put', data }),
  remove: (id: number, confirmName: string) =>
    request<null>({ url: `/clusters/${id}`, method: 'delete', data: { confirmName } }),
  refresh: (id: number) => request<RefreshResult>({ url: `/clusters/${id}/refresh`, method: 'post' }),
  batchRefresh: (ids: number[]) =>
    request<BatchRefreshResult>({ url: '/clusters/batch-refresh', method: 'post', data: { ids } }),
  overview: (id: number) => request<ClusterOverview>({ url: `/clusters/${id}/overview`, method: 'get' }),
  topology: (id: number) => request<TopologyResult>({ url: `/clusters/${id}/topology`, method: 'get' }),
  instances: (id: number, params: Record<string, unknown>) =>
    request<PageData<InstanceRow>>({ url: `/clusters/${id}/instances`, method: 'get', params }),
  components: (id: number) =>
    request<{ components: ComponentRow[] }>({ url: `/clusters/${id}/components`, method: 'get' }),
  capabilities: (id: number) =>
    request<{ capabilities: Capabilities; featureVisible: Record<string, boolean>; tidbVersion: string }>({
      url: `/clusters/${id}/capabilities`,
      method: 'get',
    }),
  changes: (id: number) =>
    request<{ list: ClusterChangeRow[] }>({ url: `/clusters/${id}/changes`, method: 'get' }),
  jobs: (id: number, limit = 5) =>
    request<{ list: JobRow[] }>({ url: `/clusters/${id}/jobs`, method: 'get', params: { limit } }),
  metrics: (id: number, range: string) =>
    request<unknown>({ url: `/clusters/${id}/metrics`, method: 'get', params: { range } }),
  /** 告警列表：后端直接返回裸数组（OK(c, list)），不套 { list } 包装。 */
  alerts: (id: number) => request<unknown[]>({ url: `/clusters/${id}/alerts`, method: 'get' }),
}

// ---------------------------------------------------------------------------
// TiDB 对象
// ---------------------------------------------------------------------------

export const tidbApi = {
  users: (id: number, params: Record<string, unknown>) =>
    request<PageData<TiDBUserRow>>({ url: `/clusters/${id}/tidb-users`, method: 'get', params }),
  createUser: (
    id: number,
    data: { userName: string; host: string; password: string; resourceGroup?: string; grants?: string[] },
  ) => request<{ jobId: number }>({ url: `/clusters/${id}/tidb-users`, method: 'post', data }),
  deleteUser: (id: number, uid: number) =>
    request<{ jobId: number }>({ url: `/clusters/${id}/tidb-users/${uid}`, method: 'delete' }),
  resetPassword: (id: number, uid: number, password: string) =>
    request<{ jobId: number }>({
      url: `/clusters/${id}/tidb-users/${uid}/password`,
      method: 'put',
      data: { password },
    }),
  grants: (id: number, uid: number) =>
    request<{ grants: string[] }>({ url: `/clusters/${id}/tidb-users/${uid}/grants`, method: 'get' }),
  changeGrants: (
    id: number,
    uid: number,
    data: { scope: string; db?: string; privs: string[]; revoke?: boolean },
  ) =>
    request<{ grants: string[] }>({
      url: `/clusters/${id}/tidb-users/${uid}/grants`,
      method: data.revoke ? 'delete' : 'post',
      data,
    }),
  resourceGroups: (id: number) =>
    request<{ list: ResourceGroupRow[] }>({ url: `/clusters/${id}/resource-groups`, method: 'get' }),
  /** 实时查询 information_schema.resource_groups（silent=true 时不弹全局错误条，由页面兜底提示）。 */
  refreshResourceGroups: (id: number, silent = false) =>
    request<{ list: ResourceGroupRow[] }>({
      url: `/clusters/${id}/resource-groups`,
      method: 'get',
      params: { live: 1 },
      headers: silent ? { 'X-TMP-Silent': '1' } : undefined,
    }),
  createRG: (id: number, data: { name: string; ruPerSec: string; priority?: string; burstable: boolean }) =>
    request<null>({ url: `/clusters/${id}/resource-groups`, method: 'post', data }),
  updateRG: (id: number, name: string, data: { ruPerSec: string; priority?: string; burstable: boolean }) =>
    request<null>({
      url: `/clusters/${id}/resource-groups/${encodeURIComponent(name)}`,
      method: 'put',
      data,
    }),
  deleteRG: (id: number, name: string) =>
    request<null>({
      url: `/clusters/${id}/resource-groups/${encodeURIComponent(name)}`,
      method: 'delete',
    }),
  bindRG: (id: number, data: { userName: string; host: string; resourceGroup: string }) =>
    request<null>({ url: `/clusters/${id}/resource-groups/bind`, method: 'post', data }),
  unbindRG: (id: number, data: { userName: string; host: string }) =>
    request<null>({ url: `/clusters/${id}/resource-groups/unbind`, method: 'post', data }),
  /** 会话快照（上次采集结果）。 */
  sessions: (id: number, params: Record<string, unknown>) =>
    request<SessionsPage>({ url: `/clusters/${id}/sessions`, method: 'get', params }),
  /** 实时查询 information_schema.cluster_processlist（silent=true 时不弹全局错误条，由页面兜底提示）。 */
  refreshSessions: (id: number, params: Record<string, unknown>, silent = false) =>
    request<SessionsPage>({
      url: `/clusters/${id}/sessions`,
      method: 'get',
      params: { ...params, live: 1 },
      headers: silent ? { 'X-TMP-Silent': '1' } : undefined,
    }),
  killSessions: (id: number, sessionIds: number[]) =>
    request<{ jobId: number }>({ url: `/clusters/${id}/sessions/kill`, method: 'post', data: { sessionIds } }),
  /** 参数快照（上次采集结果）。 */
  variables: (id: number, params: Record<string, unknown>) =>
    request<VariablesPage>({ url: `/clusters/${id}/variables`, method: 'get', params }),
  /** 实时执行 SHOW GLOBAL VARIABLES（silent=true 时不弹全局错误条，由页面兜底提示）。 */
  refreshVariables: (id: number, params: Record<string, unknown>, silent = false) =>
    request<VariablesPage>({
      url: `/clusters/${id}/variables`,
      method: 'get',
      params: { ...params, live: 1 },
      headers: silent ? { 'X-TMP-Silent': '1' } : undefined,
    }),
  updateVariable: (id: number, name: string, value: string) =>
    request<null>({ url: `/clusters/${id}/variables`, method: 'put', data: { name, value } }),
  exportVariablesUrl: (id: number, onlyDiff: boolean) =>
    `/api/v1/clusters/${id}/variables/export?onlyDiff=${onlyDiff ? 'true' : 'false'}`,
  slowQueries: (id: number, params: Record<string, unknown>) =>
    request<PageData<unknown>>({ url: `/clusters/${id}/slow-queries`, method: 'get', params }),
}

// ---------------------------------------------------------------------------
// 主机
// ---------------------------------------------------------------------------

export const hostApi = {
  list: (params: Record<string, unknown>) => request<HostsPage>({ url: '/hosts', method: 'get', params }),
  grouped: (params: Record<string, unknown>) =>
    request<{ groups: { clusterId: number; clusterName: string; hosts: HostRow[] }[] }>({
      url: '/hosts/grouped',
      method: 'get',
      params,
    }),
  get: (id: number) => request<HostRow>({ url: `/hosts/${id}`, method: 'get' }),
  metrics: (id: number, range: string) =>
    request<unknown>({ url: `/hosts/${id}/metrics`, method: 'get', params: { range } }),
  sshCheck: (hostIds: number[]) =>
    request<{ jobId: number }>({ url: '/hosts/ssh-check', method: 'post', data: { hostIds } }),
  sshChecks: (id: number) => request<{ list: unknown[] }>({ url: `/hosts/${id}/ssh-checks`, method: 'get' }),
}

// ---------------------------------------------------------------------------
// 巡检
// ---------------------------------------------------------------------------

export const inspectApi = {
  templates: () => request<{ list: InspectTemplate[] }>({ url: '/inspection/templates', method: 'get' }),
  template: (id: number) => request<InspectTemplate>({ url: `/inspection/templates/${id}`, method: 'get' }),
  items: (params: { templateId?: number; category?: string }) =>
    request<{ list: InspectItem[] }>({ url: '/inspection/items', method: 'get', params }),
  startRuns: (clusterIds: number[], templateId: number) =>
    request<{ results: { clusterId: number; clusterName: string; runId: number; runNo: string; jobId: number }[] }>({
      url: '/inspection/runs',
      method: 'post',
      data: { clusterIds, templateId },
    }),
  run: (id: number) => request<InspectRunDetail>({ url: `/inspection/runs/${id}`, method: 'get' }),
  reports: (params: Record<string, unknown>) =>
    request<PageData<InspectReport>>({ url: '/inspection/reports', method: 'get', params }),
  report: (id: number) => request<InspectReportDetail>({ url: `/inspection/reports/${id}`, method: 'get' }),
  downloadUrl: (id: number, format: 'md' | 'html') => `/api/v1/inspection/reports/${id}/download?format=${format}`,
  schedules: () => request<{ list: unknown[] }>({ url: '/inspection/schedules', method: 'get' }),
}

// ---------------------------------------------------------------------------
// 任务中心
// ---------------------------------------------------------------------------

export const jobApi = {
  list: (params: Record<string, unknown>) => request<PageData<JobRow>>({ url: '/jobs', method: 'get', params }),
  runningCount: () => request<{ count: number }>({ url: '/jobs/running-count', method: 'get' }),
  get: (id: number) => request<JobRow>({ url: `/jobs/${id}`, method: 'get' }),
  steps: (id: number) => request<{ list: JobStep[] }>({ url: `/jobs/${id}/steps`, method: 'get' }),
  logs: (id: number, afterSeq = 0) =>
    request<{ list: JobLog[] }>({ url: `/jobs/${id}/logs`, method: 'get', params: { afterSeq } }),
  retry: (id: number) => request<{ jobId: number }>({ url: `/jobs/${id}/retry`, method: 'post' }),
  cancel: (id: number) => request<null>({ url: `/jobs/${id}/cancel`, method: 'post' }),
}

// ---------------------------------------------------------------------------
// 设置 / 凭据 / 审计 / 枚举
// ---------------------------------------------------------------------------

export const settingApi = {
  collection: () => request<CollectionSettings>({ url: '/settings/collection', method: 'get' }),
  updateCollection: (data: CollectionSettings) =>
    request<CollectionSettings>({ url: '/settings/collection', method: 'put', data }),
  credentials: () => request<CredentialView[]>({ url: '/settings/credentials', method: 'get' }),
  updateCredential: (
    clusterId: number,
    data: { sshPassword?: string; sshKey?: string; dbPassword?: string },
  ) => request<null>({ url: `/settings/credentials/${clusterId}`, method: 'put', data }),
  testCredential: (clusterId: number) =>
    request<{ results: ProbeResult[]; mock: boolean }>({
      url: `/settings/credentials/${clusterId}/test`,
      method: 'post',
      timeout: 30000,
    }),
  demoMode: () => request<DemoModeStatus>({ url: '/settings/demo-mode', method: 'get' }),
  setDemoMode: (enabled: boolean) =>
    request<DemoModeStatus>({ url: '/settings/demo-mode', method: 'put', data: { enabled } }),
  about: () => request<AboutInfo>({ url: '/settings/about', method: 'get' }),
}

export const auditApi = {
  list: (params: Record<string, unknown>) =>
    request<PageData<AuditLogRow>>({ url: '/audit-logs', method: 'get', params }),
  exportUrl: '/api/v1/audit-logs/export',
}

export const metaApi = {
  enums: () => request<EnumDict>({ url: '/meta/enums', method: 'get' }),
}

/** 带 token 下载文件（浏览器原生下载无法附带 Authorization 头）。 */
export async function downloadFile(url: string, fallbackName: string): Promise<void> {
  const resp = await rawClient.get(url, { responseType: 'blob' })
  const disposition = String(resp.headers['content-disposition'] || '')
  let filename = fallbackName
  const utf8 = /filename\*=UTF-8''([^;]+)/i.exec(disposition)
  if (utf8 && utf8[1]) {
    filename = decodeURIComponent(utf8[1])
  }
  const blobUrl = window.URL.createObjectURL(new Blob([resp.data]))
  const link = document.createElement('a')
  link.href = blobUrl
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  window.URL.revokeObjectURL(blobUrl)
}
