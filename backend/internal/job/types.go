// Package job 异步任务引擎：有界队列 + worker pool + 状态机 + 步骤/日志落库 + 重启恢复。
package job

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
)

// 任务类型常量（ARCHITECTURE §7.2）。
const (
	TypeTakeover          = "takeover"
	TypeCollect           = "collect"
	TypeInspect           = "inspect"
	TypeSSHCheck          = "ssh_check"
	TypeCreateTiDBUser    = "create_tidb_user"
	TypeDropTiDBUser      = "drop_tidb_user"
	TypeKillSession       = "kill_session"
	TypeResetTiDBPassword = "reset_tidb_password"
	TypeReport            = "report"
)

// TypeLabels 任务类型展示名。
var TypeLabels = map[string]string{
	TypeTakeover:          "接管采集",
	TypeCollect:           "刷新采集",
	TypeInspect:           "集群巡检",
	TypeSSHCheck:          "SSH 连通性检测",
	TypeCreateTiDBUser:    "创建 TiDB 用户",
	TypeDropTiDBUser:      "删除 TiDB 用户",
	TypeKillSession:       "Kill 会话",
	TypeResetTiDBPassword: "重置 TiDB 用户密码",
	TypeReport:            "生成巡检报告",
}

// TypeLabel 返回任务类型中文名。
func TypeLabel(t string) string {
	if v, ok := TypeLabels[t]; ok {
		return v
	}
	return t
}

// Spec 任务规格：步骤编排与超时。
type Spec struct {
	Type    string
	Steps   []string
	Timeout time.Duration
}

// specs 全部任务类型的步骤定义。
var specs = map[string]Spec{
	TypeTakeover: {
		Type:    TypeTakeover,
		Steps:   []string{"校验参数", "SSH 连通性检查", "数据库连通性检查", "PD 探测", "读取集群拓扑", "写入元数据", "完成"},
		Timeout: 60 * time.Second,
	},
	TypeCollect: {
		Type:    TypeCollect,
		Steps:   []string{"能力探测", "SSH 拓扑采集", "MySQL 对象采集", "PD 状态采集", "归一化落库"},
		Timeout: 15 * time.Second,
	},
	TypeInspect: {
		Type:    TypeInspect,
		Steps:   []string{"加载巡检模板", "采集最新快照", "逐项判定", "汇总统计", "生成报告"},
		Timeout: 120 * time.Second,
	},
	TypeSSHCheck: {
		Type:    TypeSSHCheck,
		Steps:   []string{"解析主机", "逐主机探测", "回写 SSH 状态"},
		Timeout: 90 * time.Second,
	},
	TypeCreateTiDBUser: {
		Type:    TypeCreateTiDBUser,
		Steps:   []string{"校验标识符白名单", "预检重名", "创建用户", "授予权限", "刷新用户快照"},
		Timeout: 30 * time.Second,
	},
	TypeDropTiDBUser: {
		Type:    TypeDropTiDBUser,
		Steps:   []string{"保护账号校验", "删除用户", "刷新用户快照"},
		Timeout: 30 * time.Second,
	},
	TypeKillSession: {
		Type:    TypeKillSession,
		Steps:   []string{"拉取会话", "逐条 Kill"},
		Timeout: 30 * time.Second,
	},
	TypeResetTiDBPassword: {
		Type:    TypeResetTiDBPassword,
		Steps:   []string{"校验账号", "重置密码", "刷新用户快照"},
		Timeout: 30 * time.Second,
	},
	TypeReport: {
		Type:    TypeReport,
		Steps:   []string{"读取巡检运行", "渲染 Markdown/HTML", "写入报告"},
		Timeout: 30 * time.Second,
	},
}

// SpecOf 返回任务规格，未注册类型返回 collect 规格。
func SpecOf(jobType string) Spec {
	if s, ok := specs[jobType]; ok {
		return s
	}
	return specs[TypeCollect]
}

// IsKnownType 判断任务类型是否已注册。
func IsKnownType(t string) bool {
	_, ok := specs[t]
	return ok
}

// AllTypes 返回全部任务类型（前端下拉用）。
func AllTypes() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Type)
	}
	return out
}

// Handler 任务处理函数。
type Handler func(ctx context.Context, r *Runner) error

// ---------- 任务入参（payload） ----------

// TakeoverPayload 接管任务入参。
type TakeoverPayload struct {
	ClusterID int64 `json:"clusterId"`
}

// CollectPayload 采集任务入参。
type CollectPayload struct {
	ClusterID int64 `json:"clusterId"`
	// Scopes 为空表示全量采集。
	Scopes []string `json:"scopes,omitempty"`
}

// InspectPayload 巡检任务入参。
type InspectPayload struct {
	RunID      int64 `json:"runId"`
	ClusterID  int64 `json:"clusterId"`
	TemplateID int64 `json:"templateId"`
}

// SSHCheckPayload SSH 检测入参。
type SSHCheckPayload struct {
	HostIDs []int64 `json:"hostIds"`
}

// CreateUserPayload 创建 TiDB 用户入参。
type CreateUserPayload struct {
	ClusterID     int64    `json:"clusterId"`
	UserName      string   `json:"userName"`
	Host          string   `json:"host"`
	Password      string   `json:"password"`
	ResourceGroup string   `json:"resourceGroup"`
	Grants        []string `json:"grants"`
}

// DropUserPayload 删除 TiDB 用户入参。
type DropUserPayload struct {
	ClusterID    int64  `json:"clusterId"`
	SnapshotID   int64  `json:"snapshotId"`
	UserName     string `json:"userName"`
	Host         string `json:"host"`
	OperatorName string `json:"operatorName"`
}

// ResetPasswordPayload 重置 TiDB 用户密码入参。
type ResetPasswordPayload struct {
	ClusterID  int64  `json:"clusterId"`
	SnapshotID int64  `json:"snapshotId"`
	UserName   string `json:"userName"`
	Host       string `json:"host"`
	Password   string `json:"password"`
}

// KillSessionPayload Kill 会话入参。
type KillSessionPayload struct {
	ClusterID  int64   `json:"clusterId"`
	SessionIDs []int64 `json:"sessionIds"`
}

// ReportPayload 报告重试入参。
type ReportPayload struct {
	RunID int64 `json:"runId"`
}

// SnapshotSink 快照落库端口（由 service.ClusterService 实现）。
//
// job 包不依赖 service 包，避免循环引用；实现由 main 注入。
type SnapshotSink interface {
	PersistSnapshot(ctx context.Context, clusterID int64, snap *collector.Snapshot) error
}

// InspectRunner 巡检执行端口（由 service.InspectService 实现）。
type InspectRunner interface {
	// ExecuteRun 执行巡检：采集快照 → 逐项判定 → 汇总 → 生成报告。
	ExecuteRun(ctx context.Context, runID int64, hooks RunHooks) error
	// RegenerateReportWithHooks 仅重新生成报告（失败重试场景）。
	RegenerateReportWithHooks(ctx context.Context, runID int64, hooks RunHooks) error
}

// 巡检阶段编号，与 Spec.Steps 顺序一致。
const (
	PhaseLoadTemplate = 1
	PhaseCollect      = 2
	PhaseJudge        = 3
	PhaseSummarize    = 4
	PhaseReport       = 5
)

// RunHooks 巡检阶段回调。
type RunHooks struct {
	OnPhaseStart func(phase int)
	OnPhaseEnd   func(phase int, err error)
	OnItemDone   func(done, total int, itemName, level string)
}

// Marshal 把任意 payload 序列化为 JSON 字符串。
func Marshal(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
