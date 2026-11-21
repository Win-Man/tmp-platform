/** 领域模型类型定义（与后端 JSON 字段严格对应）。 */

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
  createdAt?: string
}

export interface ClusterOption {
  id: number
  name: string
  status: ClusterStatus
}

export interface TopologyNode {
  id: number
  hostIp: string
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

export interface InstanceRow {
  id: number
  clusterId: number
  component: string
  hostIp: string
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
  mode?: string
  cpuCount?: number
  ruPerSec?: number
  burstable?: string
  fetchedAt?: string
}

export interface SessionRow {
  id: number
  clusterId: number
  sessionId: number
  userName: string
  host: string
  db?: string
  command: string
  timeSec: number
  state?: string
  sqlText?: string
  longQuery?: boolean
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
