package auth

import "sort"

// 角色常量。
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Action 操作权限标识。每个 REST 端点独立声明所需 Action，不依赖前端隐藏。
type Action string

// 全部操作权限常量（约 35 个，覆盖 PRD 8.4 权限矩阵所有行）。
const (
	ActionOverviewView Action = "overview.view"

	ActionClusterView     Action = "cluster.view"
	ActionClusterTakeover Action = "cluster.takeover"
	ActionClusterEdit     Action = "cluster.edit"
	ActionClusterDelete   Action = "cluster.delete"
	ActionClusterRefresh  Action = "cluster.refresh"

	ActionTiDBUserView     Action = "tidb_user.view"
	ActionTiDBUserCreate   Action = "tidb_user.create"
	ActionTiDBUserDelete   Action = "tidb_user.delete"
	ActionTiDBUserPassword Action = "tidb_user.password"
	ActionTiDBUserGrant    Action = "tidb_user.grant"

	ActionRGView Action = "resource_group.view"
	ActionRGBind Action = "resource_group.bind"

	ActionSessionView Action = "session.view"
	ActionSessionKill Action = "session.kill"

	ActionVariableView   Action = "variable.view"
	ActionVariableUpdate Action = "variable.update"

	ActionInspectView Action = "inspect.view"
	ActionInspectRun  Action = "inspect.run"

	ActionHostView     Action = "host.view"
	ActionHostSSHCheck Action = "host.ssh.check"

	ActionJobView   Action = "job.view"
	ActionJobRetry  Action = "job.retry"
	ActionJobCancel Action = "job.cancel"

	ActionPlatformUserView   Action = "platform_user.view"
	ActionPlatformUserManage Action = "platform_user.manage"

	ActionCredentialView   Action = "credential.view"
	ActionCredentialManage Action = "credential.manage"

	ActionSettingView   Action = "setting.view"
	ActionSettingManage Action = "setting.manage"

	ActionDemoToggle Action = "demo.toggle"
	ActionAuditView  Action = "audit.view"

	ActionReportView     Action = "report.view"
	ActionReportDownload Action = "report.download"

	ActionRoleView Action = "role.view"
)

// ActionDef 权限矩阵中的一行：操作、展示名、分组与三种角色的授权情况。
type ActionDef struct {
	Action   Action `json:"action"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Admin    bool   `json:"admin"`
	Operator bool   `json:"operator"`
	Viewer   bool   `json:"viewer"`
}

// roleMatrix 角色 → 允许的 Action 集合（静态矩阵，与 PRD 8.4 完全一致）。
var roleMatrix = map[string]map[Action]bool{
	RoleAdmin:    allActions(),
	RoleOperator: operatorActions(),
	RoleViewer:   viewerActions(),
}

// readActions 只读类操作，admin/operator/viewer 均可。
func readActions() []Action {
	return []Action{
		ActionOverviewView,
		ActionClusterView,
		ActionTiDBUserView,
		ActionRGView,
		ActionSessionView,
		ActionVariableView,
		ActionInspectView,
		ActionHostView,
		ActionJobView,
		ActionReportView,
		ActionReportDownload,
		ActionRoleView,
		ActionSettingView,
	}
}

// writeActions 变更类操作，仅 admin/operator。
func writeActions() []Action {
	return []Action{
		ActionClusterTakeover,
		ActionClusterEdit,
		ActionClusterDelete,
		ActionClusterRefresh,
		ActionTiDBUserCreate,
		ActionTiDBUserDelete,
		ActionTiDBUserPassword,
		ActionTiDBUserGrant,
		ActionRGBind,
		ActionSessionKill,
		ActionVariableUpdate,
		ActionInspectRun,
		ActionHostSSHCheck,
		ActionJobRetry,
		ActionJobCancel,
	}
}

// adminOnlyActions 仅 admin。
func adminOnlyActions() []Action {
	return []Action{
		ActionPlatformUserView,
		ActionPlatformUserManage,
		ActionCredentialView,
		ActionCredentialManage,
		ActionSettingManage,
		ActionDemoToggle,
		ActionAuditView,
	}
}

// allActions admin 的全部权限。
func allActions() map[Action]bool {
	set := map[Action]bool{}
	for _, a := range readActions() {
		set[a] = true
	}
	for _, a := range writeActions() {
		set[a] = true
	}
	for _, a := range adminOnlyActions() {
		set[a] = true
	}
	return set
}

// operatorActions operator：只读 + 变更，不含 admin 专属。
func operatorActions() map[Action]bool {
	set := map[Action]bool{}
	for _, a := range readActions() {
		set[a] = true
	}
	for _, a := range writeActions() {
		set[a] = true
	}
	return set
}

// viewerActions viewer：仅只读。
func viewerActions() map[Action]bool {
	set := map[Action]bool{}
	for _, a := range readActions() {
		set[a] = true
	}
	return set
}

// Allows 判断角色是否拥有某操作权限。
func Allows(role string, action Action) bool {
	set, ok := roleMatrix[role]
	if !ok {
		return false
	}
	return set[action]
}

// Permissions 返回角色拥有的全部 Action 列表（字典序）。
func Permissions(role string) []Action {
	set := roleMatrix[role]
	out := make([]Action, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Matrix 返回角色 × 操作矩阵（供 GET /roles/matrix 只读展示）。
func Matrix() []ActionDef {
	return []ActionDef{
		{ActionOverviewView, "查看总览", "查看类", true, true, true},
		{ActionClusterView, "查看集群 / 实例 / 拓扑", "查看类", true, true, true},
		{ActionTiDBUserView, "查看 TiDB 用户", "查看类", true, true, true},
		{ActionVariableView, "查看参数", "查看类", true, true, true},
		{ActionSessionView, "查看会话 / 性能 / 慢查询", "查看类", true, true, true},
		{ActionHostView, "查看主机", "查看类", true, true, true},
		{ActionInspectView, "查看巡检模板与记录", "查看类", true, true, true},
		{ActionReportView, "查看巡检报告", "查看类", true, true, true},
		{ActionReportDownload, "下载巡检报告", "查看类", true, true, true},
		{ActionJobView, "查看任务列表与详情", "查看类", true, true, true},
		{ActionRoleView, "查看角色权限矩阵", "查看类", true, true, true},
		{ActionSettingView, "查看采集配置（只读）", "查看类", true, true, true},

		{ActionClusterTakeover, "接管集群", "集群变更", true, true, false},
		{ActionClusterEdit, "编辑纳管信息", "集群变更", true, true, false},
		{ActionClusterDelete, "移除纳管", "集群变更", true, true, false},
		{ActionClusterRefresh, "手动刷新采集（单/批量）", "集群变更", true, true, false},

		{ActionTiDBUserCreate, "创建 TiDB 用户", "TiDB 对象", true, true, false},
		{ActionTiDBUserDelete, "删除 TiDB 用户", "TiDB 对象", true, true, false},
		{ActionTiDBUserPassword, "重置 TiDB 用户密码", "TiDB 对象", true, true, false},
		{ActionTiDBUserGrant, "授予 / 回收权限", "TiDB 对象", true, true, false},
		{ActionRGBind, "Resource Group 绑定 / 解绑", "TiDB 对象", true, true, false},
		{ActionSessionKill, "Kill 会话", "TiDB 对象", true, true, false},
		{ActionVariableUpdate, "修改全局变量（白名单）", "TiDB 对象", true, true, false},

		{ActionInspectRun, "发起立即巡检 / 定时巡检配置", "巡检与任务", true, true, false},
		{ActionJobRetry, "任务重试", "巡检与任务", true, true, false},
		{ActionJobCancel, "任务取消", "巡检与任务", true, true, false},
		{ActionHostSSHCheck, "主机 SSH 连通性检测", "巡检与任务", true, true, false},

		{ActionPlatformUserView, "查看平台用户", "平台管理", true, false, false},
		{ActionPlatformUserManage, "平台用户与角色管理", "平台管理", true, false, false},
		{ActionCredentialView, "查看凭据列表", "平台管理", true, false, false},
		{ActionCredentialManage, "凭据管理", "平台管理", true, false, false},
		{ActionSettingManage, "采集配置修改", "平台管理", true, false, false},
		{ActionDemoToggle, "演示模式切换", "平台管理", true, false, false},
		{ActionAuditView, "审计日志查看与导出", "平台管理", true, false, false},
	}
}
