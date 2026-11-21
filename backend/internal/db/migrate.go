package db

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// Models 返回全部 19 张表对应的 GORM 模型。
func Models() []any {
	return []any{
		&model.PlatformUser{},
		&model.Cluster{},
		&model.ClusterCredential{},
		&model.Host{},
		&model.Instance{},
		&model.TiDBUser{},
		&model.ResourceGroup{},
		&model.Session{},
		&model.Variable{},
		&model.Job{},
		&model.JobStep{},
		&model.JobLog{},
		&model.InspectTemplate{},
		&model.InspectItem{},
		&model.InspectRun{},
		&model.InspectResult{},
		&model.InspectReport{},
		&model.AuditLog{},
		&model.Setting{},
	}
}

// indexDDL 显式声明的索引与唯一约束（与 ARCHITECTURE §4.1 逐条对应）。
//
// 使用 IF NOT EXISTS 保证迁移幂等。
var indexDDL = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_users_username ON platform_users(username)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_clusters_name_lower ON clusters(name_lower)`,
	`CREATE INDEX IF NOT EXISTS idx_clusters_status ON clusters(status)`,
	`CREATE INDEX IF NOT EXISTS idx_clusters_deleted ON clusters(deleted_at)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_cred_cluster ON cluster_credentials(cluster_id)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_hosts_ip ON hosts(ip)`,
	`CREATE INDEX IF NOT EXISTS idx_hosts_arch ON hosts(cpu_arch)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_instances ON instances(cluster_id, host_ip, port, component)`,
	`CREATE INDEX IF NOT EXISTS idx_instances_cluster ON instances(cluster_id)`,
	`CREATE INDEX IF NOT EXISTS idx_instances_host ON instances(host_id)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_tidb_users ON tidb_users(cluster_id, user_name, host)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_rg ON resource_groups(cluster_id, name)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_sessions ON sessions(cluster_id, session_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_variables ON variables(cluster_id, name)`,

	`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status)`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_cluster ON jobs(cluster_id)`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(type)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_job_steps ON job_steps(job_id, seq)`,
	`CREATE INDEX IF NOT EXISTS idx_job_logs ON job_logs(job_id, id)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_tpl_code ON inspect_templates(code)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_item_code ON inspect_items(template_id, code)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_runs_no ON inspect_runs(run_no)`,
	`CREATE INDEX IF NOT EXISTS idx_runs_cluster ON inspect_runs(cluster_id)`,
	`CREATE INDEX IF NOT EXISTS idx_runs_created ON inspect_runs(created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_results_run ON inspect_results(run_id, level)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_reports_no ON inspect_reports(report_no)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_reports_run ON inspect_reports(run_id)`,

	`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(username)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_logs(action)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_resource ON audit_logs(resource_type, resource_id)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_settings_key ON settings(key)`,
}

// Migrate 执行 AutoMigrate 与显式索引创建。
func Migrate(gdb *gorm.DB) error {
	if err := gdb.AutoMigrate(Models()...); err != nil {
		return fmt.Errorf("自动迁移失败: %w", err)
	}
	for _, ddl := range indexDDL {
		if err := gdb.Exec(ddl).Error; err != nil {
			return fmt.Errorf("创建索引失败(%s): %w", ddl, err)
		}
	}
	return nil
}
