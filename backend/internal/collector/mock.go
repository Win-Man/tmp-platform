package collector

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MockClusterNames 内置 3 套演示集群（ARCHITECTURE §11 mock 契约）。
var MockClusterNames = []string{"mock-prod-a", "mock-prod-b", "mock-test-c"}

// mockChannel 预置数据的单通道实现（probe 用）。
type mockChannel struct {
	kind Kind
}

// Kind 实现 Channel。
func (m *mockChannel) Kind() Kind { return m.kind }

// Probe 演示模式下恒成功。
func (m *mockChannel) Probe(_ context.Context, _ *Target) (ProbeResult, error) {
	scope := ScopeSSH
	switch m.kind {
	case KindMySQL:
		scope = ScopeMySQL
	case KindPD:
		scope = ScopePD
	}
	return ProbeResult{
		Scope:     scope,
		OK:        true,
		LatencyMS: 8,
		Message:   "演示模式：模拟连接成功",
		Detail:    map[string]any{"mock": true},
	}, nil
}

// MockCollector 演示模式采集器：直接返回预置 Snapshot，ChanErrors 恒为空。
type MockCollector struct {
	ssh   Channel
	mysql Channel
	pd    Channel
	now   func() time.Time
}

// NewMockCollector 构造演示采集器。
func NewMockCollector() *MockCollector {
	return &MockCollector{
		ssh:   &mockChannel{kind: KindSSH},
		mysql: &mockChannel{kind: KindMySQL},
		pd:    &mockChannel{kind: KindPD},
		now:   time.Now,
	}
}

// Kind 实现 Collector。
func (m *MockCollector) Kind() Kind { return KindMock }

// Probe 三个通道均返回成功。
func (m *MockCollector) Probe(ctx context.Context, t *Target, scopes []ProbeScope) ([]ProbeResult, error) {
	channels := []Channel{m.ssh, m.mysql, m.pd}
	out := make([]ProbeResult, 0, len(channels))
	for _, ch := range channels {
		if !scopeWanted(scopes, ProbeScope(ch.Kind())) {
			continue
		}
		res, err := ch.Probe(ctx, t)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// Capabilities 按 mock 集群版本返回能力集合。
func (m *MockCollector) Capabilities(_ context.Context, t *Target) (*Capabilities, error) {
	fixture := m.fixture(t.ClusterName)
	return BuildCapabilities(fixture.version, nil), nil
}

// Exec 演示模式下不执行任何真实语句。
func (m *MockCollector) Exec(_ context.Context, _ *Target, _ Statement) error { return nil }

// mockFixture 一套演示集群的原始数据。
type mockFixture struct {
	version     string
	arch        string
	deployMode  string
	instanceSet []InstanceSnapshot
	users       []TiDBUserRow
	variables   []VariableRow
	sessions    []SessionRow
	rgs         []RGRow
	hosts       []HostResource
	clusterID   string
}

// fixture 按集群名选择预置数据；未知名称退化到 mock-prod-a。
func (m *MockCollector) fixture(name string) *mockFixture {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mock-prod-b":
		return fixtureProdB()
	case "mock-test-c":
		return fixtureTestC()
	default:
		return fixtureProdA()
	}
}

// Collect 返回预置 Snapshot。
func (m *MockCollector) Collect(_ context.Context, t *Target, sc CollectScope) (*Snapshot, error) {
	f := m.fixture(t.ClusterName)
	now := m.now()
	snap := &Snapshot{
		FetchedAt:  now,
		Mock:       true,
		ChanErrors: map[Kind]string{},
	}

	meta := &TiUPMeta{}
	meta.ClusterVersion = f.version
	meta.Version = f.version
	meta.Global.Arch = f.arch
	meta.Global.GrafanaPort = 3000
	for _, inst := range f.instanceSet {
		srv := MetaServer{Host: inst.HostIP, Port: inst.Port, StatusPort: inst.StatusPort, DeployDir: inst.DeployDir, DataDir: inst.DataDir, AZ: inst.AZ}
		switch inst.Component {
		case "pd":
			meta.PdServers = append(meta.PdServers, srv)
		case "tidb":
			meta.TidbServers = append(meta.TidbServers, srv)
		case "tikv":
			meta.TikvServers = append(meta.TikvServers, srv)
		case "tiflash":
			meta.TiflashServers = append(meta.TiflashServers, srv)
		case "ticdc":
			meta.CdcServers = append(meta.CdcServers, srv)
		case "grafana":
			meta.GrafanaServers = append(meta.GrafanaServers, srv)
		case "prometheus":
			meta.MonitoringServers = append(meta.MonitoringServers, srv)
		case "alertmanager":
			meta.AlertmanagerServers = append(meta.AlertmanagerServers, srv)
		}
	}
	if sc.Meta {
		snap.Meta = meta
		snap.Instances = append([]InstanceSnapshot(nil), f.instanceSet...)
	}
	if sc.Hosts {
		snap.Hosts = append([]HostResource(nil), f.hosts...)
	}
	if sc.Users {
		snap.Users = append([]TiDBUserRow(nil), f.users...)
	}
	if sc.Variables {
		snap.Variables = append([]VariableRow(nil), f.variables...)
	}
	if sc.Sessions {
		snap.Sessions = append([]SessionRow(nil), f.sessions...)
	}
	if sc.RGs {
		snap.RGs = append([]RGRow(nil), f.rgs...)
	}
	snap.ClusterInfo = &ClusterInfo{
		ClusterID:     f.clusterID,
		TiDBVersion:   f.version,
		DeployMode:    f.deployMode,
		InstanceCount: len(f.instanceSet),
		HostCount:     countHosts(f.instanceSet),
	}
	snap.Capabilities = BuildCapabilities(f.version, nil)
	snap.DeployModeHint = f.deployMode
	snap.Stores = mockStores(f)
	// Normalized 与真实模式保持一致，供落库时读取版本 / 架构 / 外部地址。
	snap.Normalized = NormalizeMeta(meta, t.ClusterName)
	if snap.Normalized.Version == "" {
		snap.Normalized.Version = f.version
	}
	if snap.Normalized.Arch == "" {
		snap.Normalized.Arch = f.arch
	}
	return snap, nil
}

// mockStores 按 TiKV 实例生成演示用 store 容量数据。
func mockStores(f *mockFixture) []Store {
	var out []Store
	idx := 0
	for _, inst := range f.instanceSet {
		if inst.Component != "tikv" {
			continue
		}
		idx++
		// 第 3 套演示集群 tikv 水位偏高，用于触发容量类告警。
		usedRatio := 0.42 + float64(idx)*0.05
		if f.version == "v8.5.0" {
			usedRatio = 0.88
		}
		capacity := int64(2 * 1024 * 1024 * 1024 * 1024)
		available := int64(float64(capacity) * (1 - usedRatio))
		out = append(out, Store{
			Address:     inst.HostIP + ":" + strconv.Itoa(inst.Port),
			StateName:   inst.Status,
			Version:     f.version,
			Labels:      map[string]string{"zone": "zone-" + strconv.Itoa(idx)},
			LeaderCount: 120 * idx,
			RegionCount: 360 * idx,
			Capacity:    capacity,
			Available:   available,
		})
	}
	return out
}

// scopeWanted 判断探测范围是否被请求。
func scopeWanted(scopes []ProbeScope, s ProbeScope) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, v := range scopes {
		if v == s {
			return true
		}
	}
	return false
}

// mkInstances 构造实例列表。
func mkInstances(prefix string, component string, hosts []string, port, statusPort int, status, version string) []InstanceSnapshot {
	out := make([]InstanceSnapshot, 0, len(hosts))
	for i, h := range hosts {
		role := "service"
		if component == "pd" && i == 0 {
			role = "leader"
		}
		out = append(out, InstanceSnapshot{
			Component:  component,
			HostIP:     h,
			Port:       port + i,
			StatusPort: statusPort + i,
			Role:       role,
			Status:     status,
			Version:    version,
			DeployDir:  "/data/" + prefix + "/deploy/" + component + "-" + portSuffix(port),
			DataDir:    "/data/" + prefix + "/data/" + component,
		})
	}
	return out
}

// portSuffix 组件端口标签。
func portSuffix(port int) string {
	return fmt.Sprintf("%d", port)
}

// mkHosts 构造主机资源列表。
func mkHosts(prefix string, arch string, ips []string, cores int, memGB, diskGB float64, cpu, mem, disk float64) []HostResource {
	out := make([]HostResource, 0, len(ips))
	for i, ip := range ips {
		out = append(out, HostResource{
			IP:           ip,
			Hostname:     fmt.Sprintf("%s-node%d", prefix, i+1),
			OSUser:       "tidb",
			Arch:         arch,
			CPUCores:     cores,
			MemoryGB:     memGB,
			DiskTotalGB:  diskGB,
			CPUUsagePct:  cpu + float64(i)*1.3,
			MemUsagePct:  mem + float64(i)*0.8,
			DiskUsagePct: disk + float64(i)*0.5,
		})
	}
	return out
}

// baseVariables 通用变量集，差异项用于「仅看差异」验收。
func baseVariables(overrides map[string]string) []VariableRow {
	base := []VariableRow{
		{Name: "max_connections", Value: "1000", DefaultValue: "1000", Scope: "global", IsDynamic: true, Description: "最大连接数"},
		{Name: "tidb_mem_quota_query", Value: "1073741824", DefaultValue: "1073741824", Scope: "session", IsDynamic: true, Description: "单条 SQL 内存上限"},
		{Name: "oom-action", Value: "cancel", DefaultValue: "cancel", Scope: "session", IsDynamic: true, Description: "OOM 时行为"},
		{Name: "tidb_distsql_scan_concurrency", Value: "15", DefaultValue: "15", Scope: "session", IsDynamic: true, Description: "DistSQL 扫描并发度"},
		{Name: "log_slow_query_threshold", Value: "300", DefaultValue: "300", Scope: "global", IsDynamic: true, Description: "慢查询阈值(ms)"},
		{Name: "tidb_enable_async_commit", Value: "ON", DefaultValue: "ON", Scope: "global", IsDynamic: false, Description: "异步提交"},
		{Name: "tidb_txn_mode", Value: "pessimistic", DefaultValue: "pessimistic", Scope: "session", IsDynamic: true, Description: "事务模式"},
		{Name: "autocommit", Value: "ON", DefaultValue: "ON", Scope: "both", IsDynamic: true, Description: "自动提交"},
		{Name: "sql_mode", Value: "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES", DefaultValue: "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES", Scope: "both", IsDynamic: true, Description: "SQL 模式"},
		{Name: "tidb_gc_life_time", Value: "10m0s", DefaultValue: "10m0s", Scope: "global", IsDynamic: true, Description: "GC 保留时间"},
		{Name: "tidb_scatter_region", Value: "OFF", DefaultValue: "OFF", Scope: "global", IsDynamic: true, Description: "建表后打散 Region"},
		{Name: "skip_name_resolve", Value: "ON", DefaultValue: "ON", Scope: "global", IsDynamic: false, Description: "跳过域名解析"},
		{Name: "slow_query_log", Value: "ON", DefaultValue: "ON", Scope: "global", IsDynamic: true, Description: "慢日志开关"},
		{Name: "sync_binlog", Value: "1", DefaultValue: "1", Scope: "global", IsDynamic: true, Description: "binlog 落盘策略"},
		{Name: "tidb_enable_resource_control", Value: "ON", DefaultValue: "ON", Scope: "global", IsDynamic: false, Description: "Resource Control 开关"},
		{Name: "tidb_server_memory_limit", Value: "0", DefaultValue: "0", Scope: "global", IsDynamic: true, Description: "实例内存上限"},
	}
	for k, v := range overrides {
		found := false
		for i := range base {
			if base[i].Name == k {
				base[i].Value = v
				found = true
				break
			}
		}
		if !found {
			base = append(base, VariableRow{Name: k, Value: v, DefaultValue: "", Scope: "global", IsDynamic: true})
		}
	}
	sort.Slice(base, func(i, j int) bool { return base[i].Name < base[j].Name })
	return base
}

// mock-prod-a：v7.5.0 / x86_64 / DR AUTO SYNC / 12 实例 / 含 TiFlash + TiCDC。
func fixtureProdA() *mockFixture {
	version := "v7.5.0"
	ips := []string{"10.20.1.11", "10.20.1.12", "10.20.1.13", "10.20.1.14"}
	var inst []InstanceSnapshot
	inst = append(inst, mkInstances("proda", "pd", ips[:3], 2379, 2379, "up", version)...)
	inst = append(inst, mkInstances("proda", "tidb", ips[:2], 4000, 10080, "up", version)...)
	inst = append(inst, mkInstances("proda", "tikv", ips[1:], 20160, 20180, "up", version)...)
	inst = append(inst, mkInstances("proda", "tiflash", ips[2:3], 9000, 8234, "up", version)...)
	inst = append(inst, mkInstances("proda", "ticdc", ips[3:4], 8300, 8300, "up", version)...)
	inst = append(inst, mkInstances("proda", "prometheus", ips[0:1], 9090, 9090, "up", version)...)
	inst = append(inst, mkInstances("proda", "grafana", ips[0:1], 3000, 3000, "up", version)...)
	inst = append(inst, mkInstances("proda", "alertmanager", ips[1:2], 9093, 9093, "up", version)...)

	return &mockFixture{
		version:     version,
		arch:        "x86_64",
		deployMode:  "dr_auto_sync",
		instanceSet: inst,
		hosts:       mkHosts("prod-a", "x86_64", ips, 32, 128, 2000, 42.5, 61.2, 55.8),
		clusterID:   "7293456789012345678",
		users: []TiDBUserRow{
			{UserName: "root", Host: "127.0.0.1", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N", ResourceGroup: "default"},
			{UserName: "app_order", Host: "10.20.%", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N", ResourceGroup: "rg_olap"},
			{UserName: "app_pay", Host: "10.20.%", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N", ResourceGroup: "default"},
			{UserName: "readonly_bi", Host: "%", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N", ResourceGroup: "rg_olap"},
			{UserName: "dba_admin", Host: "10.20.1.11", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N", ResourceGroup: "default"},
			{UserName: "tmp_migrate", Host: "%", AuthPlugin: "mysql_native_password", AccountLocked: "Y", PasswordExpired: "N", ResourceGroup: "default"},
		},
		variables: baseVariables(map[string]string{
			"max_connections":               "3000",
			"tidb_distsql_scan_concurrency": "30",
			"tidb_scatter_region":           "ON",
		}),
		sessions: []SessionRow{
			{ID: 10501, Instance: "10.20.1.11:4000", UserName: "app_order", Host: "10.20.5.31:52344", DB: "order_db", Command: "Query", TimeSec: 182, State: "statistics", SQLDigest: "SELECT o.id, o.amount FROM orders o JOIN order_item i ON o.id=i.order_id WHERE o.created_at > ?"},
			{ID: 10502, Instance: "10.20.1.11:4000", UserName: "app_pay", Host: "10.20.5.32:41120", DB: "pay_db", Command: "Sleep", TimeSec: 12, State: "", SQLDigest: ""},
			{ID: 10503, Instance: "10.20.1.12:4000", UserName: "readonly_bi", Host: "10.20.6.7:33022", DB: "dw", Command: "Query", TimeSec: 75, State: "coprocessor", SQLDigest: "SELECT count(*) FROM fact_trade WHERE dt = ?"},
			{ID: 10504, Instance: "10.20.1.12:4000", UserName: "app_order", Host: "10.20.5.33:39210", DB: "order_db", Command: "Query", TimeSec: 3, State: "autocommit", SQLDigest: "INSERT INTO order_log (id, msg) VALUES (?, ?)"},
		},
		rgs: []RGRow{
			{Name: "default", RUPerSec: "UNLIMITED", Priority: "MEDIUM", Burstable: false, UserCount: 4, Status: "running"},
			{Name: "rg_olap", RUPerSec: "2000", Priority: "LOW", Burstable: true, UserCount: 2, Status: "running"},
			{Name: "rg_oltp", RUPerSec: "5000", Priority: "HIGH", Burstable: false, UserCount: 0, Status: "running"},
		},
	}
}

// mock-prod-b：v6.5.4 / x86_64 / Majority / 8 实例 / 无 RG 能力。
func fixtureProdB() *mockFixture {
	version := "v6.5.4"
	ips := []string{"10.30.2.21", "10.30.2.22", "10.30.2.23"}
	var inst []InstanceSnapshot
	inst = append(inst, mkInstances("prodb", "pd", ips, 2379, 2379, "up", version)...)
	inst = append(inst, mkInstances("prodb", "tidb", ips[:2], 4000, 10080, "up", version)...)
	inst = append(inst, mkInstances("prodb", "tikv", ips, 20160, 20180, "up", version)...)
	// prometheus + grafana 补齐到 8 个实例
	inst = append(inst, mkInstances("prodb", "prometheus", ips[0:1], 9090, 9090, "up", version)...)
	inst = append(inst, mkInstances("prodb", "grafana", ips[0:1], 3000, 3000, "up", version)...)

	users := []TiDBUserRow{
		{UserName: "root", Host: "127.0.0.1", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N"},
		{UserName: "app_crm", Host: "%", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N"},
		{UserName: "report_user", Host: "10.30.%", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N"},
	}
	return &mockFixture{
		version:     version,
		arch:        "x86_64",
		deployMode:  "majority",
		instanceSet: inst,
		hosts:       mkHosts("prod-b", "x86_64", ips, 16, 64, 1000, 71.4, 78.9, 82.3),
		clusterID:   "7293456789012300001",
		users:       users,
		variables: baseVariables(map[string]string{
			"max_connections":          "800",
			"slow_query_log":           "OFF",
			"tidb_enable_async_commit": "OFF",
		}),
		sessions: []SessionRow{
			{ID: 2401, Instance: "10.30.2.21:4000", UserName: "app_crm", Host: "10.30.9.11:51022", DB: "crm", Command: "Query", TimeSec: 320, State: "coprocessor", SQLDigest: "SELECT * FROM customer WHERE phone LIKE ?"},
			{ID: 2402, Instance: "10.30.2.21:4000", UserName: "report_user", Host: "10.30.9.12:41880", DB: "crm", Command: "Sleep", TimeSec: 5, State: "", SQLDigest: ""},
		},
		// v6.5 不提供 Resource Group，返回空集合。
		rgs: nil,
	}
}

// mock-test-c：v8.5.0 / aarch64 / Majority / 5 实例 / 含 1 个 Down 节点触发降级。
func fixtureTestC() *mockFixture {
	version := "v8.5.0"
	ips := []string{"10.40.3.31", "10.40.3.32", "10.40.3.33"}
	var inst []InstanceSnapshot
	inst = append(inst, mkInstances("testc", "pd", ips, 2379, 2379, "up", version)...)
	tidbs := mkInstances("testc", "tidb", ips[:2], 4000, 10080, "up", version)
	tidbs[1].Status = "down"
	inst = append(inst, tidbs...)
	inst = append(inst, mkInstances("testc", "tikv", ips, 20160, 20180, "up", version)...)

	return &mockFixture{
		version:     version,
		arch:        "aarch64",
		deployMode:  "majority",
		instanceSet: inst,
		hosts:       mkHosts("test-c", "aarch64", ips, 16, 64, 500, 88.2, 91.5, 90.1),
		clusterID:   "7293456789012300002",
		users: []TiDBUserRow{
			{UserName: "root", Host: "127.0.0.1", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N", ResourceGroup: "default"},
			{UserName: "tester", Host: "%", AuthPlugin: "mysql_native_password", AccountLocked: "N", PasswordExpired: "N", ResourceGroup: "rg_test"},
		},
		variables: baseVariables(map[string]string{
			"oom-action":               "log",
			"tidb_gc_life_time":        "24h0m0s",
			"tidb_server_memory_limit": "32GB",
		}),
		sessions: []SessionRow{
			{ID: 3311, Instance: "10.40.3.31:4000", UserName: "tester", Host: "10.40.9.21:60122", DB: "test", Command: "Query", TimeSec: 91, State: "hashagg", SQLDigest: "SELECT /*+ hash_agg */ t.id, count(*) FROM t GROUP BY t.id"},
		},
		rgs: []RGRow{
			{Name: "default", RUPerSec: "UNLIMITED", Priority: "MEDIUM", Burstable: false, UserCount: 1, Status: "running"},
			{Name: "rg_test", RUPerSec: "1000", Priority: "LOW", Burstable: true, UserCount: 1, Status: "running"},
		},
	}
}
