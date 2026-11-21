package model

import "time"

// 巡检结果分级。
const (
	LevelCritical = "critical"
	LevelWarning  = "warning"
	LevelPass     = "pass"
	LevelNA       = "na"
	LevelUnknown  = "unknown"
)

// 巡检运行状态。
const (
	InspectRunPending = "pending"
	InspectRunRunning = "running"
	InspectRunSuccess = "success"
	InspectRunFailed  = "failed"
)

// 报告状态。
const (
	ReportStatusGenerating = "generating"
	ReportStatusReady      = "ready"
	ReportStatusFailed     = "failed"
)

// InspectTemplate 巡检模板。
type InspectTemplate struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Code        string    `gorm:"column:code;type:text;not null" json:"code"`
	Name        string    `gorm:"column:name;type:text;not null" json:"name"`
	Description string    `gorm:"column:description;type:text" json:"description"`
	Builtin     int       `gorm:"column:builtin;not null;default:1" json:"builtin"`
	Enabled     int       `gorm:"column:enabled;not null;default:1" json:"enabled"`
	ItemCount   int       `gorm:"column:item_count;not null;default:0" json:"itemCount"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (InspectTemplate) TableName() string { return "inspect_templates" }

// InspectItem 巡检项定义。
type InspectItem struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TemplateID  int64     `gorm:"column:template_id;not null" json:"templateId"`
	Code        string    `gorm:"column:code;type:text;not null" json:"code"`
	Name        string    `gorm:"column:name;type:text;not null" json:"name"`
	Category    string    `gorm:"column:category;type:text;not null" json:"category"`
	CheckMethod string    `gorm:"column:check_method;type:text;not null" json:"checkMethod"`
	Severity    string    `gorm:"column:severity;type:text;not null" json:"severity"`
	Expr        string    `gorm:"column:expr;type:text;not null" json:"expr"`
	Expect      string    `gorm:"column:expect;type:text" json:"expect"`
	Suggestion  string    `gorm:"column:suggestion;type:text;not null" json:"suggestion"`
	DocURL      string    `gorm:"column:doc_url;type:text" json:"docUrl"`
	Applies     string    `gorm:"column:applies;type:text" json:"applies"`
	SortOrder   int       `gorm:"column:sort_order;not null;default:0" json:"sortOrder"`
	Enabled     int       `gorm:"column:enabled;not null;default:1" json:"enabled"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (InspectItem) TableName() string { return "inspect_items" }

// InspectRun 巡检运行。
type InspectRun struct {
	ID            int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	RunNo         string     `gorm:"column:run_no;type:text;not null" json:"runNo"`
	TemplateID    int64      `gorm:"column:template_id;not null" json:"templateId"`
	ClusterID     *int64     `gorm:"column:cluster_id" json:"clusterId"`
	ClusterName   string     `gorm:"column:cluster_name;type:text;not null" json:"clusterName"`
	TriggerType   string     `gorm:"column:trigger_type;type:text;not null;default:manual" json:"triggerType"`
	JobID         *int64     `gorm:"column:job_id" json:"jobId"`
	Status        string     `gorm:"column:status;type:text;not null;default:pending" json:"status"`
	TotalCount    int        `gorm:"column:total_count;not null;default:0" json:"totalCount"`
	CriticalCount int        `gorm:"column:critical_count;not null;default:0" json:"criticalCount"`
	WarningCount  int        `gorm:"column:warning_count;not null;default:0" json:"warningCount"`
	PassCount     int        `gorm:"column:pass_count;not null;default:0" json:"passCount"`
	NACount       int        `gorm:"column:na_count;not null;default:0" json:"naCount"`
	UnknownCount  int        `gorm:"column:unknown_count;not null;default:0" json:"unknownCount"`
	PassRate      float64    `gorm:"column:pass_rate;not null;default:0" json:"passRate"`
	Conclusion    string     `gorm:"column:conclusion;type:text" json:"conclusion"`
	StartedAt     *time.Time `gorm:"column:started_at" json:"startedAt"`
	FinishedAt    *time.Time `gorm:"column:finished_at" json:"finishedAt"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (InspectRun) TableName() string { return "inspect_runs" }

// InspectResult 巡检结果明细。
type InspectResult struct {
	ID            int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	RunID         int64     `gorm:"column:run_id;not null" json:"runId"`
	ItemCode      string    `gorm:"column:item_code;type:text;not null" json:"itemCode"`
	ItemName      string    `gorm:"column:item_name;type:text;not null" json:"itemName"`
	Category      string    `gorm:"column:category;type:text;not null" json:"category"`
	Severity      string    `gorm:"column:severity;type:text;not null" json:"severity"`
	Level         string    `gorm:"column:level;type:text;not null" json:"level"`
	ActualValue   string    `gorm:"column:actual_value;type:text" json:"actualValue"`
	ExpectedValue string    `gorm:"column:expected_value;type:text" json:"expectedValue"`
	Evidence      string    `gorm:"column:evidence;type:text" json:"evidence"`
	Suggestion    string    `gorm:"column:suggestion;type:text" json:"suggestion"`
	DurationMS    int       `gorm:"column:duration_ms;not null;default:0" json:"durationMs"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (InspectResult) TableName() string { return "inspect_results" }

// InspectReport 巡检报告。
type InspectReport struct {
	ID          int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ReportNo    string     `gorm:"column:report_no;type:text;not null" json:"reportNo"`
	RunID       int64      `gorm:"column:run_id;not null" json:"runId"`
	ClusterID   *int64     `gorm:"column:cluster_id" json:"clusterId"`
	ClusterName string     `gorm:"column:cluster_name;type:text;not null" json:"clusterName"`
	Status      string     `gorm:"column:status;type:text;not null;default:ready" json:"status"`
	ContentMD   string     `gorm:"column:content_md;type:text" json:"-"`
	ContentHTML string     `gorm:"column:content_html;type:text" json:"-"`
	FileSize    int        `gorm:"column:file_size;not null;default:0" json:"fileSize"`
	Error       string     `gorm:"column:error;type:text" json:"error"`
	GeneratedAt *time.Time `gorm:"column:generated_at" json:"generatedAt"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (InspectReport) TableName() string { return "inspect_reports" }
