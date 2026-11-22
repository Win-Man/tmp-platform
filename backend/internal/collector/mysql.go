package collector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// ErrUnsupported 当前 TiDB 版本不支持该能力（用于 Resource Group）。
var ErrUnsupported = errors.New("当前版本不支持该能力")

// mysqlChannel 通过 MySQL 协议访问 TiDB。
type mysqlChannel struct {
	timeout time.Duration
}

// NewMySQLChannel 构造 MySQL 通道。
func NewMySQLChannel(timeout time.Duration) MySQLChannel {
	if timeout <= 0 {
		timeout = ChannelTimeout
	}
	return &mysqlChannel{timeout: timeout}
}

// Kind 实现 Channel。
func (c *mysqlChannel) Kind() Kind { return KindMySQL }

// open 建立一次性连接；调用方负责 Close。
//
// 参数化查询强制 interpolateParams=false，避免驱动端字符串拼接。
// 连接池限制为 2，避免连接泄漏（§6.5）。
func (c *mysqlChannel) open(ctx context.Context, t *Target) (*sql.DB, error) {
	if t.DBHost == "" {
		return nil, errors.New("未配置数据库地址")
	}
	port := t.DBPort
	if port <= 0 {
		port = 4000
	}
	cfg := mysqldriver.NewConfig()
	cfg.User = t.DBUser
	cfg.Passwd = t.DBPassword
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(t.DBHost, strconv.Itoa(port))
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = c.timeout
	cfg.WriteTimeout = c.timeout
	cfg.ParseTime = true
	cfg.InterpolateParams = false
	cfg.AllowNativePasswords = true
	cfg.Loc = time.Local

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Probe 轻量探测：SELECT 1。
func (c *mysqlChannel) Probe(ctx context.Context, t *Target) (ProbeResult, error) {
	start := time.Now()
	res := ProbeResult{Scope: ScopeMySQL}
	db, err := c.open(ctx, t)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = "数据库连接失败: " + sanitizeDBErr(err)
		return res, nil
	}
	defer func() { _ = db.Close() }()

	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		res.Message = "数据库连通但查询失败: " + sanitizeDBErr(err)
		return res, nil
	}
	var version string
	_ = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	res.OK = true
	res.Message = "数据库连接成功"
	res.Detail = map[string]any{"version": version}
	return res, nil
}

// Capabilities 探测版本能力。
func (c *mysqlChannel) Capabilities(ctx context.Context, t *Target) (*Capabilities, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return nil, fmt.Errorf("读取版本失败: %w", err)
	}
	probeErrors := map[string]string{}
	caps := BuildCapabilities(version, probeErrors)
	if caps.SupportsResourceGroup {
		// 能力探测：真实试探 information_schema.resource_groups。
		if _, err := c.FetchResourceGroups(ctx, t); err != nil {
			caps.SupportsResourceGroup = false
			probeErrors["resource_groups"] = sanitizeDBErr(err)
		}
	}
	return caps, nil
}

// FetchClusterInfo 汇总集群基础信息。
func (c *mysqlChannel) FetchClusterInfo(ctx context.Context, t *Target) (*ClusterInfo, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	info := &ClusterInfo{}
	rows, err := c.queryMaps(ctx, db, "SELECT TYPE, INSTANCE, VERSION FROM information_schema.cluster_info")
	if err != nil {
		// cluster_info 在部分裁剪场景不可用，退化为 SELECT VERSION()。
		var version string
		if err2 := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err2 != nil {
			return nil, err
		}
		info.TiDBVersion = version
		return info, nil
	}
	hosts := map[string]bool{}
	instances := 0
	for _, r := range rows {
		if v := getStr(r, "version"); v != "" && info.TiDBVersion == "" {
			info.TiDBVersion = v
		}
		if inst := getStr(r, "instance"); inst != "" {
			instances++
			host, _ := splitHostPort(inst)
			hosts[host] = true
		}
	}
	info.InstanceCount = instances
	info.HostCount = len(hosts)

	// 集群 ID / 部署模式从 PD 无法拿到时留空，由 PD 通道补齐。
	if res, err := c.queryMaps(ctx, db, "SELECT @@max_connections AS mc"); err == nil && len(res) > 0 {
		info.SchemaVersion = getStr(res[0], "mc")
	}
	return info, nil
}

// FetchUsers 读取 mysql.user。
func (c *mysqlChannel) FetchUsers(ctx context.Context, t *Target) ([]TiDBUserRow, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// resource_group 列仅 v7.5+ 存在；缺失时退化为不含该列的查询。
	rows, err := c.queryMaps(ctx, db,
		"SELECT User, Host, plugin, account_locked, password_expired, resource_group FROM mysql.user ORDER BY User, Host")
	if err != nil {
		rows, err = c.queryMaps(ctx, db,
			"SELECT User, Host, plugin, account_locked, password_expired FROM mysql.user ORDER BY User, Host")
		if err != nil {
			return nil, err
		}
	}
	out := make([]TiDBUserRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, TiDBUserRow{
			UserName:        getStr(r, "User"),
			Host:            getStr(r, "Host"),
			AuthPlugin:      getStr(r, "plugin"),
			AccountLocked:   getStr(r, "account_locked"),
			PasswordExpired: getStr(r, "password_expired"),
			ResourceGroup:   getStr(r, "resource_group"),
		})
	}
	return out, nil
}

// FetchVariables 读取全局变量、默认值与作用域。
//
// 实现方式：SHOW GLOBAL VARIABLES 取当前值（所有版本都有），
// 再用 information_schema.variables_info 补默认值与作用域。
// 注意不要用 information_schema.cluster_config 当默认值来源：它只有
// TYPE/INSTANCE/KEY/VALUE 四列，且 KEY 是配置文件键名（mem-quota-query），
// 与系统变量名（tidb_mem_quota_query）对不上，早年按此实现导致默认值恒为空、
// 「仅看差异」永远 0 条。
func (c *mysqlChannel) FetchVariables(ctx context.Context, t *Target) ([]VariableRow, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := c.queryMaps(ctx, db, "SHOW GLOBAL VARIABLES")
	if err != nil {
		return nil, err
	}
	// 默认值 / 作用域（v6.5+ 提供；老版本查不到就退化为空默认值）。
	meta := map[string]VariableRow{}
	if info, ierr := c.queryMaps(ctx, db,
		"SELECT VARIABLE_NAME, VARIABLE_SCOPE, DEFAULT_VALUE FROM information_schema.variables_info"); ierr == nil {
		for _, r := range info {
			name := getStr(r, "variable_name")
			if name == "" {
				continue
			}
			meta[name] = VariableRow{
				DefaultValue: getStr(r, "default_value"),
				Scope:        normalizeScope(getStr(r, "variable_scope")),
			}
		}
	}
	out := make([]VariableRow, 0, len(rows))
	for _, r := range rows {
		name := getStr(r, "variable_name")
		value := getStr(r, "value")
		m := meta[name]
		scope := m.Scope
		if scope == "" {
			scope = "both"
		}
		out = append(out, VariableRow{
			Name:         name,
			Value:        value,
			DefaultValue: m.DefaultValue,
			Scope:        scope,
			IsDynamic:    true,
			Description:  "",
		})
	}
	return out, nil
}

// normalizeScope 把 variables_info 的 VARIABLE_SCOPE 归一到前端使用的作用域取值。
func normalizeScope(v string) string {
	s := strings.ToUpper(strings.TrimSpace(v))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "GLOBAL") && strings.Contains(s, "SESSION"):
		return "both"
	case strings.Contains(s, "GLOBAL"):
		return "global"
	case strings.Contains(s, "SESSION"):
		return "session"
	}
	return "both"
}

// FetchSessions 读取集群级 processlist。
//
// 首选 information_schema.cluster_processlist：一次查询覆盖所有 TiDB 实例，
// 并带 INSTANCE 列（形如 10.10.10.10:4000），这是「会话」页面的权威来源。
// 该表需要 PROCESS 权限、且由 TiDB 4.0+ 提供；失败时退回只覆盖当前实例的
// information_schema.processlist，用连接地址兜底 INSTANCE。
//
// 同时取 INFO（SQL 原文）与 DIGEST（SQL 指纹）：DIGEST 是 TiDB 6.0 之后
// 才有的列，老版本查询会报 unknown column，因此按「带 DIGEST → 不带 DIGEST」
// 回退，保证新老版本都能采到会话。
func (c *mysqlChannel) FetchSessions(ctx context.Context, t *Target) ([]SessionRow, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, clusterWide, err := c.sessionRows(ctx, db)
	if err != nil {
		return nil, err
	}
	fallbackInstance := net.JoinHostPort(t.DBHost, strconv.Itoa(effectivePort(t)))
	out := make([]SessionRow, 0, len(rows))
	for _, r := range rows {
		id, _ := strconv.ParseInt(getStr(r, "id"), 10, 64)
		tsec, _ := strconv.Atoi(getStr(r, "time"))
		instance := getStr(r, "instance")
		if instance == "" && !clusterWide {
			instance = fallbackInstance
		}
		out = append(out, SessionRow{
			ID:        id,
			Instance:  instance,
			UserName:  getStr(r, "user"),
			Host:      getStr(r, "host"),
			DB:        getStr(r, "db"),
			Command:   getStr(r, "command"),
			TimeSec:   tsec,
			State:     getStr(r, "state"),
			SQLText:   truncateRunes(getStr(r, "info"), 200),
			SQLDigest: truncateRunes(getStr(r, "digest"), 200),
		})
	}
	return out, nil
}

// sessionRows 依次尝试 cluster_processlist（带/不带 DIGEST）→ processlist（带/不带 DIGEST）。
//
// 返回的 bool 表示是否来自集群级视图（决定 INSTANCE 列是否存在）。
func (c *mysqlChannel) sessionRows(ctx context.Context, db *sql.DB) ([]map[string]string, bool, error) {
	candidates := []struct {
		sql     string
		cluster bool
	}{
		{"SELECT INSTANCE, ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO, DIGEST FROM information_schema.cluster_processlist", true},
		{"SELECT INSTANCE, ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM information_schema.cluster_processlist", true},
		{"SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO, DIGEST FROM information_schema.processlist", false},
		{"SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO FROM information_schema.processlist", false},
	}
	var lastErr error
	for _, cand := range candidates {
		rows, err := c.queryMaps(ctx, db, cand.sql)
		if err == nil {
			return rows, cand.cluster, nil
		}
		lastErr = err
	}
	return nil, false, lastErr
}

// effectivePort 返回实际连接的端口（默认 4000）。
func effectivePort(t *Target) int {
	if t.DBPort > 0 {
		return t.DBPort
	}
	return 4000
}

// FetchResourceGroups 读取 Resource Group；低版本返回 ErrUnsupported。
func (c *mysqlChannel) FetchResourceGroups(ctx context.Context, t *Target) ([]RGRow, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := c.queryMaps(ctx, db, "SELECT * FROM information_schema.resource_groups")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, sanitizeDBErr(err))
	}
	userCount := map[string]int{}
	if ur, uErr := c.queryMaps(ctx, db,
		"SELECT resource_group, COUNT(*) AS cnt FROM mysql.user WHERE resource_group IS NOT NULL GROUP BY resource_group"); uErr == nil {
		for _, r := range ur {
			n, _ := strconv.Atoi(getStr(r, "cnt"))
			userCount[getStr(r, "resource_group")] = n
		}
	}
	out := make([]RGRow, 0, len(rows))
	for _, r := range rows {
		name := getStr(r, "name")
		if name == "" {
			continue
		}
		burstable := false
		if v := strings.ToLower(getStr(r, "burstable")); v == "true" || v == "1" || v == "yes" {
			burstable = true
		}
		out = append(out, RGRow{
			Name:      name,
			RUPerSec:  getStr(r, "ru_per_sec"),
			Priority:  getStr(r, "priority"),
			Burstable: burstable,
			UserCount: userCount[name],
			Status:    getStr(r, "status"),
		})
	}
	return out, nil
}

// Exec 执行白名单写语句（参数化）。
func (c *mysqlChannel) Exec(ctx context.Context, t *Target, st Statement) error {
	db, err := c.open(ctx, t)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if st.SQL == "" {
		return errors.New("空语句")
	}
	if _, err := db.ExecContext(ctx, st.SQL, st.Args...); err != nil {
		return fmt.Errorf("执行失败: %w", err)
	}
	return nil
}

// QueryMaps 供 service 层做只读查询（如 SHOW GRANTS）。
func (c *mysqlChannel) QueryMaps(ctx context.Context, t *Target, query string, args ...any) ([]map[string]string, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return c.queryMaps(ctx, db, query, args...)
}

// FetchHostHardware 采集集群内所有主机的硬件规格与负载。
//
// 数据来源（列结构同构，均为 TYPE/INSTANCE/DEVICE_TYPE/DEVICE_NAME/NAME/VALUE）：
//   - information_schema.cluster_hardware：cpu-arch / cpu-logical-cores / cpu-physical-cores、
//     内存 capacity、磁盘 path/total/used/used-percent；
//   - information_schema.cluster_load：cpu/usage/idle、memory/virtual/used-percent。
//
// 语义约定（ARCHITECTURE §6.6.4）：
//   - cluster_hardware 失败直接返回 error（编排层吞掉并降级，不影响整次采集）；
//   - cluster_load 失败只跳过（使用率保持 0），因为硬件规格仍然可用；
//   - 行 → []HostResource 的聚合逻辑在 hardware.go:AggregateHostHardware（纯函数，可单测）。
func (c *mysqlChannel) FetchHostHardware(ctx context.Context, t *Target) ([]HostResource, error) {
	db, err := c.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	hw, err := c.queryMaps(ctx, db,
		"SELECT INSTANCE, DEVICE_TYPE, DEVICE_NAME, NAME, VALUE FROM information_schema.cluster_hardware")
	if err != nil {
		return nil, fmt.Errorf("读取主机硬件信息失败: %s", sanitizeDBErr(err))
	}
	// 负载表需要单独的权限/版本支持，失败只降级不报错。
	//
	// 带上 TYPE 列：一台机器上往往同时有 tidb/tikv/pd 多个实例，聚合时需要优先取
	// tidb 实例的负载（hardware.go: pickLoadSamples）。若个别版本没有该列，
	// 退化为不带 TYPE 的查询——此时聚合按「INSTANCE 最小」的确定性规则取数。
	load, loadErr := c.queryMaps(ctx, db,
		"SELECT TYPE, INSTANCE, DEVICE_TYPE, DEVICE_NAME, NAME, VALUE FROM information_schema.cluster_load")
	if loadErr != nil {
		load, loadErr = c.queryMaps(ctx, db,
			"SELECT INSTANCE, DEVICE_TYPE, DEVICE_NAME, NAME, VALUE FROM information_schema.cluster_load")
	}
	if loadErr != nil {
		load = nil
	}
	return AggregateHostHardware(hw, load), nil
}

// queryMaps 把任意结果集扫描为 map[string]string（键统一小写）。
func (c *mysqlChannel) queryMaps(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	raw := make([]sql.RawBytes, len(cols))
	scanArgs := make([]any, len(cols))
	for i := range raw {
		scanArgs[i] = &raw[i]
	}
	out := make([]map[string]string, 0, 16)
	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		m := make(map[string]string, len(cols))
		for i, c := range cols {
			if raw[i] == nil {
				m[strings.ToLower(c)] = ""
				continue
			}
			m[strings.ToLower(c)] = string(raw[i])
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// getStr 从 map 取值（键小写）。
func getStr(m map[string]string, key string) string {
	return m[strings.ToLower(key)]
}

// truncateRunes 按字符截断。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// sanitizeDBErr 去掉错误信息中的账号密码。
func sanitizeDBErr(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeCredential(err.Error())
}

// sanitizeCredential 替换 user:pass@ 形态。
func sanitizeCredential(s string) string {
	at := strings.LastIndex(s, "@")
	if at <= 0 {
		return s
	}
	colon := strings.Index(s, ":")
	if colon <= 0 || colon > at {
		return s
	}
	return s[:colon+1] + "***" + s[at:]
}
