/** 领域模型类型定义（与后端 JSON 字段严格对应）。 */

import type { PageData } from './http'

export type ClusterStatus = 'healthy' | 'degraded' | 'error' | 'unknown'
export type CollectStatus = 'healthy' | 'degraded' | 'error' | 'unknown'
export type JobStatus = 'pending' | 'running' | 'success' | 'failed' | 'canceled' | 'interrupted'

export interface UserProfile {
  id: number
  username: string
  displayName: string
  role: 'admin' | 'operator' | 'viewer'
  status: 'active' | 'disabled'
  mustChangePassword: boolean
  lastLoginAt?: string | null
  lastLoginIp?: string
  createdAt?: string
  permissions?: string[]
}

export interface LoginResult {
  token: string
  expiresAt: string
  user: UserProfile
}

export interface OverviewSummary {
  clusterTotal: number
  instanceTotal: number
  hostTotal: number
  abnormalTotal: number
  /** 异常实例总数（实例状态非 up）。 */
  instanceAbnormalTotal: number
  tidbVersionDist: Record<string, number>
  mockMode: boolean
}

export interface HealthItem {
  status: string
  count: number
}

export interface LatestInspection {
  found: boolean
  clusterId?: number | null
  clusterName?: string
  runId?: number
  runNo?: string
  passRate?: number
  criticalCount?: number
  warningCount?: number
  conclusion?: string
  reportId?: number
  executedAt?: string | null
}

export interface CollectStatusInfo {
  lastCollectAt?: string | null
  successCount: number
  failedCount: number
  mockMode: boolean
  dataSource: string
}

export interface ClusterBrief {
  id: number
  name: string
  draft: boolean
  /** 演示模式采集的集群（内置样例数据，非真实集群）。 */
  demo: boolean
  tidbVersion: string
  deployMode: string
  cpuArch: string
  tiupHost: string
  tiupPort: number
  tiupUser: string
  componentCount: number
  hostCount: number
  instanceCount: number
  status: ClusterStatus
  collectStatus: CollectStatus
  collectFailStreak: number
  collecting: boolean
  alertCount: number
  /** 最高告警级别（critical / warning / info），未接入告警时为空。 */
  alertMaxLevel?: string
  /** 非 up 状态实例数（down / unhealthy / unknown）。 */
  abnormalInstanceCount: number
  lastCollectAt?: string | null
  lastSuccessAt?: string | null
  fetchedAt?: string | null
  lastCollectError?: string
  /** 最近一次采集的元数据来源：display_json / display_text / meta_yaml。 */
  collectSource?: string
  /** 交叉校验告警：TiUP 报告的实例状态与平台实测连通性矛盾时逐条记录。 */
  collectWarnings?: string[]
  createdAt?: string
}

export interface ClusterOption {
  id: number
  name: string
  status: ClusterStatus
  /** 中控机地址：不同中控机上允许存在同名集群，下拉项需要靠它区分。 */
  tiupHost?: string
  tidbVersion?: string
  draft?: boolean
}

/** 集群地址映射规则：把集群内网平面地址映射为 TMP 可达地址。 */
export interface AddrMapRow {
  id: number
  clusterId: number
  /** 源地址：精确 IP（10.10.9.11）、CIDR（10.10.9.0/24）或简写前缀（10.10.9.）。 */
  source: string
  /** 目标地址，形态需与 source 一致。 */
  target: string
  note?: string
  /** 1 启用 / 0 停用。 */
  enabled: number
  createdBy?: number | null
  createdAt?: string
  updatedAt?: string
}

/** 地址映射试算结果。 */
export interface AddrMapPreview {
  input: string
  mapped: string
  matched: boolean
  source: string
  target: string
}

export interface TopologyNode {
  id: number
  hostIp: string
  /** 采集到的原始地址；地址映射生效时才返回（供界面标注内网地址）。 */
  rawHostIp?: string
  /** 实例所属组件（pd / tidb / tikv / ...），按主机视角渲染时必需。 */
  component: string
  port: number
  statusPort: number
  status: string
  role: string
  version: string
  az?: string
}

export interface TopologyGroup {
  component: string
  nodes: TopologyNode[]
  nodeCount?: number
}

/** 按主机聚合的拓扑：一台主机承载的所有实例。 */
export interface TopologyHost {
  hostIp: string
  /** 采集到的原始地址；地址映射生效时才返回。 */
  rawHostIp?: string
  hostname: string
  /** ok / fail / unknown */
  sshStatus: string
  cpuCores: number
  memoryGb: number
  instanceCount: number
  components: string[]
  instances: TopologyNode[]
}

export interface TopologyResult {
  hosts: TopologyHost[]
  groups: TopologyGroup[]
  nodeCount: number
  hostCount: number
}

export interface InstanceRow {
  id: number
  clusterId: number
  component: string
  hostIp: string
  /** 采集到的原始地址；地址映射生效时才返回。 */
  rawHostIp?: string
  port: number
  statusPort: number
  status: string
  role: string
  version: string
  az?: string
  fetchedAt?: string
}

export interface ComponentRow {
  component: string
  instanceCount: number
  versions: string[]
  versionConsistent: boolean
}

export interface Capabilities {
  tidbVersion: string
  supportsResourceGroup: boolean
  supportsSessionKill: boolean
  supportsPlacementRules: boolean
  probedAt?: string
}

export interface TiDBUserRow {
  id: number
  clusterId: number
  userName: string
  host: string
  authPlugin?: string
  accountLocked?: string
  passwordExpired?: string
  resourceGroup?: string | null
  fetchedAt?: string
}

export interface ResourceGroupRow {
  id: number
  clusterId: number
  name: string
  ruPerSec?: string
  priority?: string
  burstable?: number
  userCount?: number
  status?: string
  fetchedAt?: string
}

export interface SessionRow {
  id: number
  clusterId: number
  sessionId: number
  /** 会话所在 TiDB 实例（ip:port），来自 cluster_processlist.INSTANCE。 */
  instance?: string
  userName: string
  host: string
  db?: string
  command: string
  timeSec: number
  state?: string
  /** 正在执行的 SQL 原文（processlist.INFO），空闲会话为空。 */
  sqlText?: string
  /** SQL 指纹（processlist.DIGEST），同一条语句的归一化结果。 */
  sqlDigest?: string
  fetchedAt?: string
}

/** 会话列表：live=true 表示实时查询结果。 */
export interface SessionsPage extends PageData<SessionRow> {
  live?: boolean
  source?: string
  fetchedAt?: string
}

export interface VariableRow {
  id: number
  clusterId: number
  name: string
  value: string
  defaultValue: string
  scope: string
  isDynamic: number
  description?: string
  differs: number
  fetchedAt?: string
}

/** 参数列表：live=true 表示实时执行 SHOW GLOBAL VARIABLES 的结果。 */
export interface VariablesPage extends PageData<VariableRow> {
  live?: boolean
  source?: string
  fetchedAt?: string
}

/** 主机所属集群的精简信息（含中控机，用于区分同名集群）。 */
export interface HostClusterBrief {
  id: number
  name: string
  tiupHost: string
}

export interface HostRow {
  id: number
  ip: string
  hostname: string
  osUser: string
  cpuArch: string
  cpuCores: number
  memoryGb: number
  diskTotalGb: number
  cpuUsagePct: number
  memUsagePct: number
  diskUsagePct: number
  sshStatus: string
  sshCheckedAt?: string | null
  sshError?: string
  resourceFetchedAt?: string | null
  instanceCount?: number
  roles?: string[]
  clusterNames?: string[]
  clusterIds?: number[]
  /** 所属集群明细：一台主机可同时属于多套集群，且不同中控机上可能有同名集群。 */
  clusters?: HostClusterBrief[]
  /** 地址映射后的可达地址（仅当能确定所属集群且规则命中时返回）。 */
  mappedIp?: string
}

export interface JobRow {
  id: number
  type: string
  typeLabel?: string
  clusterId?: number | null
  targetType?: string
  targetLabel?: string
  status: JobStatus
  progress: number
  currentStep?: string
  errorSummary?: string
  result?: string
  cancelRequested?: number
  retryOf?: number | null
  createdBy?: number | null
  createdByName?: string
  startedAt?: string | null
  finishedAt?: string | null
  createdAt?: string
  durationMs?: number
}

export interface JobStep {
  id: number
  jobId: number
  seq: number
  name: string
  status: string
  progress: number
  error?: string
  startedAt?: string | null
  finishedAt?: string | null
}

export interface JobLog {
  seq: number
  jobId: number
  stepSeq: number
  level: string
  message: string
  at: string
}

export interface InspectItem {
  id?: number
  templateId?: number
  code: string
  name: string
  category: string
  categoryLabel?: string
  checkMethod: string
  severity: string
  expect: string
  suggestion: string
  docUrl?: string
  sortOrder?: number
  applies?: string
}

export interface InspectTemplate {
  id: number
  code: string
  name: string
  description: string
  builtin: boolean | number
  enabled: boolean | number
  itemCount: number
  categories?: { category: string; label: string; count: number }[]
  items?: InspectItem[]
}

export interface InspectRun {
  id: number
  runNo: string
  templateId: number
  clusterId: number
  clusterName: string
  triggerType: string
  jobId: number
  status: string
  totalCount: number
  criticalCount: number
  warningCount: number
  passCount: number
  naCount: number
  unknownCount: number
  passRate: number
  conclusion: string
  startedAt?: string | null
  finishedAt?: string | null
  createdAt?: string
}

export interface InspectResultRow {
  id: number
  runId: number
  itemCode: string
  itemName: string
  category: string
  level: string
  actual: string
  expect: string
  suggestion: string
  detail?: string
  sortOrder?: number
}

export interface InspectReport {
  id: number
  reportNo: string
  runId: number
  runNo: string
  clusterId: number
  clusterName: string
  templateName: string
  status: string
  triggerType: string
  passRate: number
  criticalCount: number
  warningCount: number
  passCount: number
  totalCount: number
  conclusion: string
  fileSize: number
  executedAt?: string | null
  generatedAt?: string | null
  createdAt?: string
  clusterRemoved?: boolean
}

export interface AuditLogRow {
  id: number
  userId?: number | null
  username: string
  role?: string
  action: string
  resourceType?: string
  resourceId?: string
  resourceName?: string
  result: string
  detail?: string
  ip?: string
  userAgent?: string
  requestId?: string
  createdAt: string
}

export interface CollectionSettings {
  intervalSec: number
  timeoutSec: number
  concurrency: number
  retry: number
  jwtTtlHours: number
  idleTimeoutMin: number
  retentionJobsDays: number
  retentionAuditDays: number
  thresholdCpuPct: number
  thresholdDiskPct: number
  clusterErrorStreak: number
  mockMode: boolean
  mockForced: boolean
}

export interface DemoModeStatus {
  enabled: boolean
  forced: boolean
  dataSource: string
  mockClusters: string[]
}

export interface CredentialView {
  clusterId: number
  clusterName: string
  tiupHost: string
  tiupUser: string
  dbHost: string
  dbPort: number
  dbUser: string
  hasSshPassword: boolean
  hasSshKey: boolean
  hasDbPassword: boolean
  updatedAt?: string | null
  updatedBy?: number | null
}

export interface ProbeResult {
  scope: string
  ok: boolean
  latencyMs: number
  message: string
}

export interface AboutInfo {
  version: string
  buildTime: string
  commit: string
  database: string
  goVersion: string
  mockMode: boolean
  compatibility: {
    version: string
    basicInfo: boolean
    tidbUser: boolean
    resourceGroup: boolean
    session: boolean
    sessionKill: boolean
    variables: boolean
    variableUpdate: boolean
    note: string
  }[]
  settings: Record<string, string>
}

export interface EnumOption {
  value: string
  label: string
}

export interface EnumDict {
  clusterStatus: EnumOption[]
  collectStatus: EnumOption[]
  instanceStatus: EnumOption[]
  deployMode: EnumOption[]
  cpuArch: EnumOption[]
  jobStatus: EnumOption[]
  jobType: EnumOption[]
  stepStatus: EnumOption[]
  role: EnumOption[]
  userStatus: EnumOption[]
  inspectLevel: EnumOption[]
  inspectCategory: EnumOption[]
  checkMethod: EnumOption[]
  severity: EnumOption[]
  variableWhitelist: string[]
  probeScope: EnumOption[]
}

export interface RoleMatrixRow {
  action: string
  name: string
  group: string
  admin: boolean
  operator: boolean
  viewer: boolean
}
