package db

import (
	"path/filepath"
	"testing"

	"github.com/tmp-platform/backend/internal/model"
)

// TestMigrate_ClusterIdentityIndex 验证集群唯一键从 name_lower 升级为 (tiup_host, name_lower)。
//
// 业务背景：同一批机器上可以部署多套 TiDB 集群，不同中控机上出现同名集群是合法形态；
// 只有「同一台中控机上的同名集群」才是重复纳管。旧的全唯一索引 uk_clusters_name_lower
// 会让第二套同名集群直接写不进去，存量库升级时必须自动换成新索引。
func TestMigrate_ClusterIdentityIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster_identity.db")
	gdb, err := Open(path, nil)
	if err != nil {
		t.Fatalf("打开元数据库失败: %v", err)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("初始化迁移失败: %v", err)
	}

	// 改造成「旧版本」：只有 name_lower 的全唯一索引。
	if err := gdb.Exec("DROP INDEX IF EXISTS uk_clusters_host_name").Error; err != nil {
		t.Fatalf("删除新索引失败: %v", err)
	}
	if err := gdb.Exec("CREATE UNIQUE INDEX uk_clusters_name_lower ON clusters(name_lower)").Error; err != nil {
		t.Fatalf("创建旧索引失败: %v", err)
	}
	// 旧索引下同名不同中控机必须写不进去（确认这条用例真的能区分新旧行为）。
	legacy := []model.Cluster{
		{Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.11", TiUPUser: "tidb"},
		{Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.12", TiUPUser: "tidb"},
	}
	if err := gdb.Create(&legacy[0]).Error; err != nil {
		t.Fatalf("写入第一个集群失败: %v", err)
	}
	if err := gdb.Create(&legacy[1]).Error; err == nil {
		t.Fatal("旧索引下同名不同中控机应当写入失败（用例前提不成立）")
	}
	// 清干净，交给迁移后的新索引路径验证。
	if err := gdb.Exec("DELETE FROM clusters").Error; err != nil {
		t.Fatalf("清空集群表失败: %v", err)
	}

	// 迁移应释放旧索引并建立 (tiup_host, name_lower) 新索引。
	if err := Migrate(gdb); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if !hasIndex(gdb, "clusters", "uk_clusters_host_name") {
		t.Fatal("迁移后缺少 uk_clusters_host_name 索引")
	}
	if hasIndex(gdb, "clusters", "uk_clusters_name_lower") {
		t.Fatal("迁移后旧索引 uk_clusters_name_lower 应已释放")
	}

	// 同名不同 tiup_host：可共存。
	rows := []model.Cluster{
		{Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.11", TiUPUser: "tidb", Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown},
		{Name: "TiDB-PROD", NameLower: "tidb-prod", TiUPHost: "10.0.0.12", TiUPUser: "tidb", Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown},
	}
	for i := range rows {
		if err := gdb.Create(&rows[i]).Error; err != nil {
			t.Fatalf("写入同名不同中控机集群失败（应允许）: %v", err)
		}
	}

	// 同一中控机同名（大小写不敏感）：必须被唯一索引拒绝。
	dup := model.Cluster{
		Name: "TIDB-PROD", NameLower: "tidb-prod", TiUPHost: "10.0.0.11", TiUPUser: "tidb",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := gdb.Create(&dup).Error; err == nil {
		t.Fatal("同一中控机上重复集群名应当写入失败")
	}

	// 迁移幂等：重复执行不报错，索引仍然在。
	if err := Migrate(gdb); err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
	if !hasIndex(gdb, "clusters", "uk_clusters_host_name") {
		t.Fatal("二次迁移后 uk_clusters_host_name 索引丢失")
	}

	// 移除纳管后同名可重新接管：name_lower 被改写成 name#removed#id。
	if err := gdb.Model(&model.Cluster{}).Where("id = ?", rows[0].ID).
		Update("name_lower", "tidb-prod#removed#1").Error; err != nil {
		t.Fatalf("改写 name_lower 失败: %v", err)
	}
	again := model.Cluster{
		Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.11", TiUPUser: "tidb",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := gdb.Create(&again).Error; err != nil {
		t.Fatalf("移除纳管后同名集群应可重新接管: %v", err)
	}
}

// TestMigrate_ClusterIdentityIndexDropsLegacyWhenBothPresent 验证新旧索引并存时迁移仍会释放旧键。
//
// 现实中的「异常库」：某次中途失败 / 手工修库之后，uk_clusters_host_name 与
// uk_clusters_name_lower 同时存在。若迁移只判断「新索引已存在」就 early-return，
// 旧的全唯一键会留着继续拦住「同名不同中控机」的写入（需求 A 等于没生效）。
func TestMigrate_ClusterIdentityIndexDropsLegacyWhenBothPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster_identity_both.db")
	gdb, err := Open(path, nil)
	if err != nil {
		t.Fatalf("打开元数据库失败: %v", err)
	}
	if err := Migrate(gdb); err != nil {
		t.Fatalf("初始化迁移失败: %v", err)
	}
	// 手工把旧的全唯一键加回来（表为空，可以建），构造「新旧并存」的异常库。
	if err := gdb.Exec("CREATE UNIQUE INDEX uk_clusters_name_lower ON clusters(name_lower)").Error; err != nil {
		t.Fatalf("构造新旧索引并存失败: %v", err)
	}
	if !hasIndex(gdb, "clusters", "uk_clusters_host_name") || !hasIndex(gdb, "clusters", "uk_clusters_name_lower") {
		t.Fatal("用例前提不成立：应当同时存在新旧两个索引")
	}

	// 前提校验：并存状态下，同名不同中控机确实会被旧索引拦住（否则这个用例没意义）。
	first := model.Cluster{
		Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.11", TiUPUser: "tidb",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := gdb.Create(&first).Error; err != nil {
		t.Fatalf("写入第一个集群失败: %v", err)
	}
	second := model.Cluster{
		Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.12", TiUPUser: "tidb",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := gdb.Create(&second).Error; err == nil {
		t.Fatal("新旧索引并存时同名不同中控机应当写入失败（用例前提不成立）")
	}

	// 迁移必须无条件释放旧索引，即使新索引已经存在。
	if err := Migrate(gdb); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if hasIndex(gdb, "clusters", "uk_clusters_name_lower") {
		t.Fatal("迁移后旧索引 uk_clusters_name_lower 必须消失")
	}
	if !hasIndex(gdb, "clusters", "uk_clusters_host_name") {
		t.Fatal("迁移后 uk_clusters_host_name 必须仍然存在")
	}

	// 旧键释放后，同名不同中控机可以共存。
	if err := gdb.Create(&second).Error; err != nil {
		t.Fatalf("释放旧索引后同名不同中控机应可写入: %v", err)
	}
	// 同一中控机同名仍然被拒。
	dup := model.Cluster{
		Name: "TIDB-PROD", NameLower: "tidb-prod", TiUPHost: "10.0.0.11", TiUPUser: "tidb",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := gdb.Create(&dup).Error; err == nil {
		t.Fatal("同一中控机上重复集群名应当写入失败")
	}
	// 幂等。
	if err := Migrate(gdb); err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
}

// TestMigrate_SessionIndexUpgrade 验证 uk_sessions 从两列升级为三列。
//
// 改用 cluster_processlist 后，session_id 只保证实例内唯一，
// 旧索引 (cluster_id, session_id) 会把不同 TiDB 实例的同号会话判为冲突。
// 存量库升级时必须能自动重建索引，否则会话落库直接失败。
func TestMigrate_SessionIndexUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migrate.db")
	gdb, err := Open(path, nil)
	if err != nil {
		t.Fatalf("打开元数据库失败: %v", err)
	}

	// 先建表（Open 只建连接，不建表）。
	if err := Migrate(gdb); err != nil {
		t.Fatalf("初始化迁移失败: %v", err)
	}

	// 再改造成「旧版本」索引。
	if err := gdb.Exec("DROP INDEX IF EXISTS uk_sessions").Error; err != nil {
		t.Fatalf("删除旧索引失败: %v", err)
	}
	if err := gdb.Exec("CREATE UNIQUE INDEX uk_sessions ON sessions(cluster_id, session_id)").Error; err != nil {
		t.Fatalf("创建旧索引失败: %v", err)
	}

	// 迁移应自动把索引升级成含 instance 的三列版本。
	if err := Migrate(gdb); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	type idxCol struct {
		Name string `gorm:"column:name"`
	}
	var cols []idxCol
	if err := gdb.Raw("SELECT name FROM pragma_index_info('uk_sessions')").Scan(&cols).Error; err != nil {
		t.Fatalf("读取索引定义失败: %v", err)
	}
	hasInstance := false
	for _, c := range cols {
		if c.Name == "instance" {
			hasInstance = true
		}
	}
	if !hasInstance {
		t.Fatalf("索引列 = %+v，期望包含 instance", cols)
	}

	// 两个不同实例上的同号会话必须共存。
	rows := []model.Session{
		{ClusterID: 1, SessionID: 100, Instance: "10.0.0.1:4000"},
		{ClusterID: 1, SessionID: 100, Instance: "10.0.0.2:4000"},
	}
	for _, r := range rows {
		if err := gdb.Create(&r).Error; err != nil {
			t.Fatalf("写入同号会话（不同实例）失败: %v", err)
		}
	}

	// 同一实例同号会话仍应被唯一索引拒绝。
	dup := model.Session{ClusterID: 1, SessionID: 100, Instance: "10.0.0.1:4000"}
	if err := gdb.Create(&dup).Error; err == nil {
		t.Fatal("同一实例内重复 session_id 应当写入失败")
	}

	// 迁移必须幂等：重复执行不报错。
	if err := Migrate(gdb); err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
}
