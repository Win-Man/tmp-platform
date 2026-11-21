package model

import "time"

// 任务状态枚举。
const (
	JobStatusPending     = "pending"
	JobStatusRunning     = "running"
	JobStatusSuccess     = "success"
	JobStatusFailed      = "failed"
	JobStatusCanceled    = "canceled"
	JobStatusInterrupted = "interrupted"
)

// 任务步骤状态枚举。
const (
	StepStatusPending = "pending"
	StepStatusRunning = "running"
	StepStatusSuccess = "success"
	StepStatusFailed  = "failed"
	StepStatusSkipped = "skipped"
)

// Job 异步任务。
type Job struct {
	ID              int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Type            string     `gorm:"column:type;type:text;not null" json:"type"`
	ClusterID       *int64     `gorm:"column:cluster_id" json:"clusterId"`
	TargetType      string     `gorm:"column:target_type;type:text" json:"targetType"`
	TargetLabel     string     `gorm:"column:target_label;type:text" json:"targetLabel"`
	Status          string     `gorm:"column:status;type:text;not null;default:pending" json:"status"`
	Progress        int        `gorm:"column:progress;not null;default:0" json:"progress"`
	CurrentStep     string     `gorm:"column:current_step;type:text" json:"currentStep"`
	ErrorSummary    string     `gorm:"column:error_summary;type:text" json:"errorSummary"`
	Payload         string     `gorm:"column:payload;type:text" json:"-"`
	Result          string     `gorm:"column:result;type:text" json:"result"`
	CancelRequested int        `gorm:"column:cancel_requested;not null;default:0" json:"cancelRequested"`
	RetryOf         *int64     `gorm:"column:retry_of" json:"retryOf"`
	CreatedBy       *int64     `gorm:"column:created_by" json:"createdBy"`
	CreatedByName   string     `gorm:"column:created_by_name;type:text" json:"createdByName"`
	StartedAt       *time.Time `gorm:"column:started_at" json:"startedAt"`
	FinishedAt      *time.Time `gorm:"column:finished_at" json:"finishedAt"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (Job) TableName() string { return "jobs" }

// JobStep 任务步骤。
type JobStep struct {
	ID         int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	JobID      int64      `gorm:"column:job_id;not null" json:"jobId"`
	Seq        int        `gorm:"column:seq;not null" json:"seq"`
	Name       string     `gorm:"column:name;type:text;not null" json:"name"`
	Status     string     `gorm:"column:status;type:text;not null;default:pending" json:"status"`
	Progress   int        `gorm:"column:progress;not null;default:0" json:"progress"`
	Error      string     `gorm:"column:error;type:text" json:"error"`
	StartedAt  *time.Time `gorm:"column:started_at" json:"startedAt"`
	FinishedAt *time.Time `gorm:"column:finished_at" json:"finishedAt"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (JobStep) TableName() string { return "job_steps" }

// JobLog 任务日志（独立表，避免 job_steps 行膨胀）。
type JobLog struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"seq"`
	JobID     int64     `gorm:"column:job_id;not null" json:"jobId"`
	StepSeq   *int      `gorm:"column:step_seq" json:"stepSeq"`
	Level     string    `gorm:"column:level;type:text;not null;default:info" json:"level"`
	Message   string    `gorm:"column:message;type:text;not null" json:"message"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"at"`
}

// TableName 固定表名。
func (JobLog) TableName() string { return "job_logs" }
