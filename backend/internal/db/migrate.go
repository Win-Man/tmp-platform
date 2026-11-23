package db

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// Models 返回全部表对应的 GORM 模型。
func Models() []any {
	return []any{
		&model.PlatformUser{},
		&model.Cluster{},
		&model.ClusterCredential{},
		&model.ClusterAddrMap{},
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

	// uk_clusters_host_name 由 ensureClusterIdentityIndex 单独维护
	// （需要先释放旧的全唯一键 uk_clusters_name_lower）。
	`CREATE INDEX IF NOT EXISTS idx_clusters_status ON clusters(status)`,
	`CREATE INDEX IF NOT EXISTS idx_clusters_deleted ON clusters(deleted_at)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_cred_cluster ON cluster_credentials(cluster_id)`,

	// 同一集群内一个源地址只能有一条规则（规范化后按源地址判重）。
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_addr_maps ON cluster_addr_maps(cluster_id, source)`,
	`CREATE INDEX IF NOT EXISTS idx_addr_maps_cluster ON cluster_addr_maps(cluster_id)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_hosts_ip ON hosts(ip)`,
	`CREATE INDEX IF NOT EXISTS idx_hosts_arch ON hosts(cpu_arch)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_instances ON instances(cluster_id, host_ip, port, component)`,
	`CREATE INDEX IF NOT EXISTS idx_instances_cluster ON instances(cluster_id)`,
	`CREATE INDEX IF NOT EXISTS idx_instances_host ON instances(host_id)`,

	`CREATE UNIQUE INDEX IF NOT EXISTS uk_tidb_users ON tidb_users(cluster_id, user_name, host)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uk_rg ON resource_groups(cluster_id, name)`,
	// uk_sessions 由 ensureSessionIndex 单独维护（需要兼容旧的两列版本）。
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
	if err := ensureSessionIndex(gdb); err != nil {
		return err
	}
	if err := ensureClusterIdentityIndex(gdb); err != nil {
		return err
	}
	for _, ddl := range indexDDL {
		if err := gdb.Exec(ddl).Error; err != nil {
			return fmt.Errorf("创建索引失败(%s): %w", ddl, err)
		}
	}
	return nil
}

// ensureClusterIdentityIndex 维护集群业务唯一键 (tiup_host, name_lower)。
//
// 同一批机器上可以部署多套 TiDB 集群，不同中控机（tiup_host）上出现同名集群是合法形态；
// 只有「同一台中控机上的同名集群」才算重复纳管。旧的全唯一索引
// uk_clusters_name_lower(name_lower) 会让第二套同名集群直接写不进去，必须释放。
//
// 注意：释放旧索引是**无条件**执行的（DROP ... IF EXISTS 本身幂等），不能放在
// 「新索引已存在」的 early-return 之后——异常库里可能出现新旧索引并存的状态，
// 此时若直接返回，旧索引仍然会拦住「同名不同中控机」的写入。
func ensureClusterIdentityIndex(gdb *gorm.DB) error {
	// 旧库可能仍带着全唯一键；不释放的话新索引建得出来但插入仍会撞旧索引。
	if err := gdb.Exec("DROP INDEX IF EXISTS uk_clusters_name_lower").Error; err != nil {
		return fmt.Errorf("释放旧集群名唯一索引失败: %w", err)
	}
	if hasIndex(gdb, "clusters", "uk_clusters_host_name") {
		return nil
	}
	if err := gdb.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uk_clusters_host_name ON clusters(tiup_host, name_lower)").Error; err != nil {
		return fmt.Errorf("创建集群唯一索引失败: %w", err)
	}
	return nil
}

// hasIndex 判断表上是否存在指定名称的索引（按 PRAGMA index_list 查询，兼容 SQLite）。
//
// table 只接受内部常量（本包内仅传 "clusters"），故此处直接拼串；
// SQLite 的表值函数不支持参数绑定。查询失败按「不存在」处理：
// 后续 DROP/CREATE 都带 IF (NOT) EXISTS，重复执行仍然安全。
func hasIndex(gdb *gorm.DB, table, index string) bool {
	var rows []struct {
		Name string `gorm:"column:name"`
	}
	if err := gdb.Raw(fmt.Sprintf("SELECT name FROM pragma_index_list('%s')", table)).Scan(&rows).Error; err != nil {
		return false
	}
	for _, r := range rows {
		if r.Name == index {
			return true
		}
	}
	return false
}

// ensureSessionIndex 维护会话唯一索引 (cluster_id, instance, session_id)。
//
// 改用 information_schema.cluster_processlist 后，session_id 只在实例内唯一，
// 旧的两列索引 (cluster_id, session_id) 会让不同 TiDB 实例的同号会话互相冲突。
// 存量库需要重建索引，故此处按 PRAGMA 检查后按需 DROP + CREATE。
func ensureSessionIndex(gdb *gorm.DB) error {
	var cols []struct {
		Name string `gorm:"column:name"`
	}
	if err := gdb.Raw("SELECT name FROM pragma_index_info('uk_sessions')").Scan(&cols).Error; err != nil {
		return fmt.Errorf("检查会话索引失败: %w", err)
	}
	hasInstance := false
	for _, c := range cols {
		if c.Name == "instance" {
			hasInstance = true
			break
		}
	}
	if hasInstance {
		return nil
	}
	if len(cols) > 0 {
		if err := gdb.Exec("DROP INDEX IF EXISTS uk_sessions").Error; err != nil {
			return fmt.Errorf("重建会话索引失败: %w", err)
		}
	}
	if err := gdb.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uk_sessions ON sessions(cluster_id, instance, session_id)").Error; err != nil {
		return fmt.Errorf("创建会话索引失败: %w", err)
	}
	return nil
}
