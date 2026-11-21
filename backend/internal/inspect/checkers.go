package inspect

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/model"
)

// Thresholds 巡检阈值（来自 settings）。
type Thresholds struct {
	CPUPct  float64
	DiskPct float64
}

// DefaultThresholds 默认阈值。
func DefaultThresholds() Thresholds {
	return Thresholds{CPUPct: 80, DiskPct: 85}
}

// Input 判定函数入参。
type Input struct {
	Cluster  *model.Cluster
	Snapshot *collector.Snapshot
	Caps     *collector.Capabilities
	Thresh   Thresholds
	Now      time.Time
}

// Result 判定结果。
type Result struct {
	Level      string
	Actual     string
	Expected   string
	Evidence   string
	Suggestion string
}

// Checker 判定函数签名。
type Checker func(in *Input) Result

// pass 构造通过结果。
func pass(actual, expected, evidence string) Result {
	return Result{Level: model.LevelPass, Actual: actual, Expected: expected, Evidence: evidence}
}

// warn 构造警告结果。
func warn(actual, expected, evidence string) Result {
	return Result{Level: model.LevelWarning, Actual: actual, Expected: expected, Evidence: evidence}
}

// fail 构造严重结果。
func fail(actual, expected, evidence string) Result {
	return Result{Level: model.LevelCritical, Actual: actual, Expected: expected, Evidence: evidence}
}

// na 构造「不适用」结果。
func na(reason string) Result {
	return Result{Level: model.LevelNA, Actual: "不适用", Expected: "", Evidence: reason}
}

// unknown 构造「未知」结果。
func unknown(reason string) Result {
	return Result{Level: model.LevelUnknown, Actual: "未知", Expected: "", Evidence: reason}
}

// checkers 判定函数注册表：code → Checker（D-3）。
var checkers = map[string]Checker{
	"SYS-01": checkCPUUsage,
	"SYS-02": checkMemUsage,
	"SYS-03": checkDiskUsage,
	"SYS-04": checkArchConsistency,
	"SYS-05": checkSnapshotFreshness,

	"CFG-01": checkMemQuotaQuery,
	"CFG-02": checkOOMAction,
	"CFG-03": checkSlowQueryLog,
	"CFG-04": checkSlowQueryThreshold,
	"CFG-05": checkMaxConnections,
	"CFG-06": checkDistSQLConcurrency,
	"CFG-07": checkEnableResourceControl,
	"CFG-08": checkServerMemoryLimit,

	"SEC-01": checkRootHost,
	"SEC-02": checkWildcardHost,
	"SEC-03": checkUserCount,
	"SEC-04": checkSkipNameResolve,
	"SEC-05": checkLockedAccounts,

	"CAP-01": checkTiKVWaterLevel,
	"CAP-02": checkInstanceDensity,
	"CAP-03": checkHostCount,

	"HA-01": checkPDReplicas,
	"HA-02": checkTiKVReplicas,
	"HA-03": checkTiDBReplicas,
	"HA-04": checkInstanceStatus,
	"HA-05": checkDeployMode,

	"VER-01": checkVersionBaseline,
	"VER-02": checkVersionConsistency,
}

// Lookup 按 code 取判定函数。
func Lookup(code string) (Checker, bool) {
	c, ok := checkers[code]
	return c, ok
}

// varValue 从快照中读取参数值。
func varValue(snap *collector.Snapshot, name string) (string, bool) {
	if snap == nil {
		return "", false
	}
	for _, v := range snap.Variables {
		if v.Name == name {
			return v.Value, true
		}
	}
	return "", false
}

// fmtFloat 格式化浮点数（一位小数）。
func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// ---------- 系统层 ----------

// checkCPUUsage 主机 CPU 使用率。
func checkCPUUsage(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Hosts) == 0 {
		return unknown("未采集到主机资源数据")
	}
	maxVal := 0.0
	worstHost := ""
	for _, h := range in.Snapshot.Hosts {
		if h.CPUUsagePct > maxVal {
			maxVal = h.CPUUsagePct
			worstHost = h.IP
		}
	}
	actual := fmt.Sprintf("最高 %s%%（%s）", fmtFloat(maxVal), worstHost)
	expected := fmt.Sprintf("全部主机 < %s%%", fmtFloat(in.Thresh.CPUPct))
	evidence := buildHostEvidence(in.Snapshot.Hosts, func(h collector.HostResource) string {
		return fmt.Sprintf("%s cpu=%s%%", h.IP, fmtFloat(h.CPUUsagePct))
	})
	if maxVal >= in.Thresh.CPUPct {
		return warn(actual, expected, evidence)
	}
	return pass(actual, expected, evidence)
}

// checkMemUsage 主机内存使用率。
func checkMemUsage(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Hosts) == 0 {
		return unknown("未采集到主机资源数据")
	}
	maxVal := 0.0
	worst := ""
	for _, h := range in.Snapshot.Hosts {
		if h.MemUsagePct > maxVal {
			maxVal = h.MemUsagePct
			worst = h.IP
		}
	}
	actual := fmt.Sprintf("最高 %s%%（%s）", fmtFloat(maxVal), worst)
	expected := "全部主机 < 85%"
	evidence := buildHostEvidence(in.Snapshot.Hosts, func(h collector.HostResource) string {
		return fmt.Sprintf("%s mem=%s%%", h.IP, fmtFloat(h.MemUsagePct))
	})
	if maxVal >= 85 {
		return warn(actual, expected, evidence)
	}
	return pass(actual, expected, evidence)
}

// checkDiskUsage 主机磁盘使用率。
func checkDiskUsage(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Hosts) == 0 {
		return unknown("未采集到主机资源数据")
	}
	maxVal := 0.0
	worst := ""
	for _, h := range in.Snapshot.Hosts {
		if h.DiskUsagePct > maxVal {
			maxVal = h.DiskUsagePct
			worst = h.IP
		}
	}
	actual := fmt.Sprintf("最高 %s%%（%s）", fmtFloat(maxVal), worst)
	expected := fmt.Sprintf("全部主机 < %s%%", fmtFloat(in.Thresh.DiskPct))
	evidence := buildHostEvidence(in.Snapshot.Hosts, func(h collector.HostResource) string {
		return fmt.Sprintf("%s disk=%s%% (total %sGB)", h.IP, fmtFloat(h.DiskUsagePct), fmtFloat(h.DiskTotalGB))
	})
	if maxVal >= in.Thresh.DiskPct {
		return fail(actual, expected, evidence)
	}
	return pass(actual, expected, evidence)
}

// checkArchConsistency CPU 架构一致性。
func checkArchConsistency(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Hosts) == 0 {
		return unknown("未采集到主机架构信息")
	}
	set := map[string]bool{}
	for _, h := range in.Snapshot.Hosts {
		if h.Arch != "" {
			set[h.Arch] = true
		}
	}
	if len(set) == 0 {
		return unknown("主机架构字段为空")
	}
	archs := make([]string, 0, len(set))
	for a := range set {
		archs = append(archs, a)
	}
	sort.Strings(archs)
	actual := strings.Join(archs, ", ")
	if len(set) > 1 {
		return warn(actual, "单一架构", "存在混合架构部署")
	}
	return pass(actual, "单一架构", "全部主机架构为 "+actual)
}

// checkSnapshotFreshness 采集数据新鲜度。
func checkSnapshotFreshness(in *Input) Result {
	if in.Snapshot == nil || in.Snapshot.FetchedAt.IsZero() {
		return unknown("无采集时间")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	diff := now.Sub(in.Snapshot.FetchedAt)
	actual := fmt.Sprintf("%.0f 分钟前", diff.Minutes())
	if diff > time.Hour {
		return warn(actual, "1 小时内", "fetchedAt="+in.Snapshot.FetchedAt.Format(time.RFC3339))
	}
	return pass(actual, "1 小时内", "fetchedAt="+in.Snapshot.FetchedAt.Format(time.RFC3339))
}

// buildHostEvidence 拼接主机证据串。
func buildHostEvidence(hosts []collector.HostResource, f func(collector.HostResource) string) string {
	parts := make([]string, 0, len(hosts))
	for _, h := range hosts {
		parts = append(parts, f(h))
	}
	return strings.Join(parts, "; ")
}

// ---------- TiDB 配置 ----------

// checkMemQuotaQuery 单条 SQL 内存上限。
func checkMemQuotaQuery(in *Input) Result {
	v, ok := varValue(in.Snapshot, "tidb_mem_quota_query")
	if !ok {
		return unknown("未采集到 tidb_mem_quota_query")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return unknown("tidb_mem_quota_query 值无法解析: " + v)
	}
	if n <= 0 {
		return warn(v, "> 0", "tidb_mem_quota_query="+v)
	}
	return pass(v, "> 0", "tidb_mem_quota_query="+v)
}

// checkOOMAction OOM 行为。
func checkOOMAction(in *Input) Result {
	v, ok := varValue(in.Snapshot, "oom-action")
	if !ok {
		if v2, ok2 := varValue(in.Snapshot, "tidb_mem_oom_action"); ok2 {
			v, ok = v2, true
		}
	}
	if !ok {
		return unknown("未采集到 oom-action")
	}
	if strings.EqualFold(v, "cancel") {
		return pass(v, "cancel", "oom-action="+v)
	}
	return fail(v, "cancel", "oom-action="+v)
}

// checkSlowQueryLog 慢日志开关。
func checkSlowQueryLog(in *Input) Result {
	v, ok := varValue(in.Snapshot, "slow_query_log")
	if !ok {
		if v2, ok2 := varValue(in.Snapshot, "tidb_enable_slow_log"); ok2 {
			v, ok = v2, true
		}
	}
	if !ok {
		return unknown("未采集到 slow_query_log")
	}
	if strings.EqualFold(v, "ON") || v == "1" {
		return pass(v, "ON", "slow_query_log="+v)
	}
	return warn(v, "ON", "slow_query_log="+v)
}

// checkSlowQueryThreshold 慢查询阈值。
func checkSlowQueryThreshold(in *Input) Result {
	v, ok := varValue(in.Snapshot, "log_slow_query_threshold")
	if !ok {
		if v2, ok2 := varValue(in.Snapshot, "tidb_slow_log_threshold"); ok2 {
			v, ok = v2, true
		}
	}
	if !ok {
		return unknown("未采集到慢查询阈值")
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return unknown("慢查询阈值无法解析: " + v)
	}
	if n > 300 {
		return warn(v+"ms", "<= 300ms", "log_slow_query_threshold="+v)
	}
	return pass(v+"ms", "<= 300ms", "log_slow_query_threshold="+v)
}

// checkMaxConnections 最大连接数。
func checkMaxConnections(in *Input) Result {
	v, ok := varValue(in.Snapshot, "max_connections")
	if !ok {
		return unknown("未采集到 max_connections")
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return unknown("max_connections 无法解析: " + v)
	}
	if n < 1000 {
		return warn(v, ">= 1000", "max_connections="+v)
	}
	return pass(v, ">= 1000", "max_connections="+v)
}

// checkDistSQLConcurrency DistSQL 扫描并发度。
func checkDistSQLConcurrency(in *Input) Result {
	v, ok := varValue(in.Snapshot, "tidb_distsql_scan_concurrency")
	if !ok {
		return unknown("未采集到 tidb_distsql_scan_concurrency")
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return unknown("值无法解析: " + v)
	}
	if n < 8 {
		return warn(v, ">= 8", "tidb_distsql_scan_concurrency="+v)
	}
	return pass(v, ">= 8", "tidb_distsql_scan_concurrency="+v)
}

// checkEnableResourceControl 资源管控开关（v7.5+）。
func checkEnableResourceControl(in *Input) Result {
	v, ok := varValue(in.Snapshot, "tidb_enable_resource_control")
	if !ok {
		return unknown("未采集到 tidb_enable_resource_control")
	}
	if strings.EqualFold(v, "ON") || v == "1" {
		return pass(v, "ON", "tidb_enable_resource_control="+v)
	}
	return warn(v, "ON", "tidb_enable_resource_control="+v)
}

// checkServerMemoryLimit 实例内存上限（v7.5+）。
func checkServerMemoryLimit(in *Input) Result {
	v, ok := varValue(in.Snapshot, "tidb_server_memory_limit")
	if !ok {
		return unknown("未采集到 tidb_server_memory_limit")
	}
	if v == "" || v == "0" {
		return warn(v, "非 0 值", "tidb_server_memory_limit="+v)
	}
	return pass(v, "非 0 值", "tidb_server_memory_limit="+v)
}

// ---------- 安全合规 ----------

// checkRootHost root 账号 Host 限制。
func checkRootHost(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Users) == 0 {
		return unknown("未采集到账号列表")
	}
	var bad []string
	for _, u := range in.Snapshot.Users {
		if strings.EqualFold(u.UserName, "root") && u.Host == "%" {
			bad = append(bad, u.UserName+"@"+u.Host)
		}
	}
	if len(bad) > 0 {
		return fail(strings.Join(bad, ", "), "root 仅限本地", "发现 root@% 远程登录账号")
	}
	return pass("root 仅本地登录", "root 仅限本地", "未发现 root@% 账号")
}

// checkWildcardHost 通配 Host 业务账号。
func checkWildcardHost(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Users) == 0 {
		return unknown("未采集到账号列表")
	}
	var bad []string
	for _, u := range in.Snapshot.Users {
		if u.Host == "%" && !strings.EqualFold(u.UserName, "root") {
			bad = append(bad, u.UserName+"@"+u.Host)
		}
	}
	if len(bad) > 0 {
		return warn(strconv.Itoa(len(bad))+" 个", "0 个", "通配 Host 账号: "+strings.Join(bad, ", "))
	}
	return pass("0 个", "0 个", "无 % 通配 Host 的业务账号")
}

// checkUserCount 账号数量规模。
func checkUserCount(in *Input) Result {
	if in.Snapshot == nil {
		return unknown("未采集到账号列表")
	}
	n := len(in.Snapshot.Users)
	actual := strconv.Itoa(n) + " 个"
	if n > 50 {
		return warn(actual, "<= 50 个", fmt.Sprintf("账号总数 %d", n))
	}
	return pass(actual, "<= 50 个", fmt.Sprintf("账号总数 %d", n))
}

// checkSkipNameResolve 域名解析开关。
func checkSkipNameResolve(in *Input) Result {
	v, ok := varValue(in.Snapshot, "skip_name_resolve")
	if !ok {
		return unknown("未采集到 skip_name_resolve")
	}
	if strings.EqualFold(v, "ON") || v == "1" {
		return pass(v, "ON", "skip_name_resolve="+v)
	}
	return warn(v, "ON", "skip_name_resolve="+v)
}

// checkLockedAccounts 锁定账号残留。
func checkLockedAccounts(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Users) == 0 {
		return unknown("未采集到账号列表")
	}
	var locked []string
	for _, u := range in.Snapshot.Users {
		if strings.EqualFold(u.AccountLocked, "Y") {
			locked = append(locked, u.UserName+"@"+u.Host)
		}
	}
	if len(locked) > 0 {
		return warn(strconv.Itoa(len(locked))+" 个", "0 个", "锁定账号: "+strings.Join(locked, ", "))
	}
	return pass("0 个", "0 个", "无锁定状态账号")
}

// ---------- 容量水位 ----------

// checkTiKVWaterLevel TiKV 存储水位。
func checkTiKVWaterLevel(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Stores) == 0 {
		return na("PD 通道未返回 store 容量数据，无法计算存储水位")
	}
	var capacity, available int64
	for _, s := range in.Snapshot.Stores {
		capacity += s.Capacity
		available += s.Available
	}
	if capacity <= 0 {
		return unknown("store 容量为 0")
	}
	usedRatio := 1 - float64(available)/float64(capacity)
	actual := fmt.Sprintf("%s%%", fmtFloat(usedRatio*100))
	expected := fmt.Sprintf("< %s%%", fmtFloat(in.Thresh.DiskPct))
	evidence := fmt.Sprintf("capacity=%.1fGiB available=%.1fGiB",
		float64(capacity)/(1<<30), float64(available)/(1<<30))
	if usedRatio*100 >= in.Thresh.DiskPct {
		return fail(actual, expected, evidence)
	}
	return pass(actual, expected, evidence)
}

// checkInstanceDensity 单机实例密度。
func checkInstanceDensity(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Instances) == 0 {
		return unknown("未采集到实例列表")
	}
	perHost := map[string]int{}
	for _, i := range in.Snapshot.Instances {
		perHost[i.HostIP]++
	}
	maxVal := 0
	worst := ""
	for ip, n := range perHost {
		if n > maxVal {
			maxVal = n
			worst = ip
		}
	}
	actual := fmt.Sprintf("最高 %d 个（%s）", maxVal, worst)
	expected := "<= 8 个/台"
	if maxVal > 8 {
		return warn(actual, expected, fmt.Sprintf("%s 承载 %d 个实例", worst, maxVal))
	}
	return pass(actual, expected, fmt.Sprintf("共 %d 台主机承载 %d 个实例", len(perHost), len(in.Snapshot.Instances)))
}

// checkHostCount 主机数量。
func checkHostCount(in *Input) Result {
	if in.Snapshot == nil {
		return unknown("未采集到主机数据")
	}
	n := len(in.Snapshot.Hosts)
	actual := strconv.Itoa(n) + " 台"
	if n < 3 {
		return warn(actual, ">= 3 台", fmt.Sprintf("主机数 %d", n))
	}
	return pass(actual, ">= 3 台", fmt.Sprintf("主机数 %d", n))
}

// ---------- 高可用 ----------

// countComponent 统计某组件实例数。
func countComponent(in *Input, component string) int {
	if in.Snapshot == nil {
		return 0
	}
	n := 0
	for _, i := range in.Snapshot.Instances {
		if i.Component == component {
			n++
		}
	}
	return n
}

// checkPDReplicas PD 副本数。
func checkPDReplicas(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Instances) == 0 {
		return unknown("未采集到实例列表")
	}
	n := countComponent(in, "pd")
	actual := strconv.Itoa(n) + " 个"
	if n < 3 {
		return fail(actual, ">= 3 个", fmt.Sprintf("PD 实例数 %d", n))
	}
	return pass(actual, ">= 3 个", fmt.Sprintf("PD 实例数 %d", n))
}

// checkTiKVReplicas TiKV 副本数。
func checkTiKVReplicas(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Instances) == 0 {
		return unknown("未采集到实例列表")
	}
	n := countComponent(in, "tikv")
	actual := strconv.Itoa(n) + " 个"
	if n < 3 {
		return fail(actual, ">= 3 个", fmt.Sprintf("TiKV 实例数 %d", n))
	}
	return pass(actual, ">= 3 个", fmt.Sprintf("TiKV 实例数 %d", n))
}

// checkTiDBReplicas TiDB 实例数。
func checkTiDBReplicas(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Instances) == 0 {
		return unknown("未采集到实例列表")
	}
	n := countComponent(in, "tidb")
	actual := strconv.Itoa(n) + " 个"
	if n < 2 {
		return warn(actual, ">= 2 个", fmt.Sprintf("TiDB 实例数 %d", n))
	}
	return pass(actual, ">= 2 个", fmt.Sprintf("TiDB 实例数 %d", n))
}

// checkInstanceStatus 实例运行状态。
func checkInstanceStatus(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Instances) == 0 {
		return unknown("未采集到实例列表")
	}
	var bad []string
	for _, i := range in.Snapshot.Instances {
		if i.Status != "up" {
			bad = append(bad, fmt.Sprintf("%s@%s:%d(%s)", i.Component, i.HostIP, i.Port, i.Status))
		}
	}
	if len(bad) > 0 {
		return fail(strconv.Itoa(len(bad))+" 个异常", "全部 up", strings.Join(bad, ", "))
	}
	return pass("全部 up", "全部 up", fmt.Sprintf("共 %d 个实例", len(in.Snapshot.Instances)))
}

// checkDeployMode 部署模式。
func checkDeployMode(in *Input) Result {
	mode := ""
	if in.Cluster != nil {
		mode = in.Cluster.DeployMode
	}
	if mode == "" && in.Snapshot != nil {
		mode = in.Snapshot.DeployModeHint
	}
	if mode == "" || mode == "unknown" {
		return na("未采集到部署模式（PD 通道未返回副本模式）")
	}
	if mode == "majority" || mode == "dr_auto_sync" {
		return pass(mode, "majority / dr_auto_sync", "deploy_mode="+mode)
	}
	return warn(mode, "majority / dr_auto_sync", "deploy_mode="+mode)
}

// ---------- 版本规范 ----------

// checkVersionBaseline 版本基线。
func checkVersionBaseline(in *Input) Result {
	version := ""
	if in.Snapshot != nil && in.Snapshot.ClusterInfo != nil {
		version = in.Snapshot.ClusterInfo.TiDBVersion
	}
	if version == "" && in.Cluster != nil {
		version = in.Cluster.TiDBVersion
	}
	if version == "" {
		return unknown("未采集到 TiDB 版本")
	}
	major, minor, ok := collector.ParseVersion(version)
	if !ok {
		return unknown("版本号无法解析: " + version)
	}
	if collector.CompareVersion(major, minor, 6, 5) < 0 {
		return fail(version, ">= v6.5.0", "tidb_version="+version)
	}
	return pass(version, ">= v6.5.0", "tidb_version="+version)
}

// checkVersionConsistency 组件版本一致性。
func checkVersionConsistency(in *Input) Result {
	if in.Snapshot == nil || len(in.Snapshot.Instances) == 0 {
		return unknown("未采集到实例列表")
	}
	set := map[string]bool{}
	for _, i := range in.Snapshot.Instances {
		if i.Version != "" {
			set[i.Version] = true
		}
	}
	if len(set) == 0 {
		return unknown("实例版本字段为空")
	}
	versions := make([]string, 0, len(set))
	for v := range set {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	actual := strings.Join(versions, ", ")
	if len(set) > 1 {
		return warn(actual, "版本一致", "存在多版本组件: "+actual)
	}
	return pass(actual, "版本一致", "全部实例版本为 "+actual)
}
