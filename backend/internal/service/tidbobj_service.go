package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/pkg/sqlsafe"
	"github.com/tmp-platform/backend/internal/repo"
)

// variableWhitelist 允许通过平台修改的全局变量白名单（D-1）。
var variableWhitelist = map[string]bool{
	"max_connections":               true,
	"oom-action":                    true,
	"tidb_mem_quota_query":          true,
	"tidb_distsql_scan_concurrency": true,
	"log_slow_query_threshold":      true,
}

// VariableWhitelist 返回白名单列表（前端展示可编辑项）。
func VariableWhitelist() []string {
	out := make([]string, 0, len(variableWhitelist))
	for k := range variableWhitelist {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TiDBObjService TiDB 对象服务（用户 / 参数 / 会话 / Resource Group）。
type TiDBObjService struct {
	clusters *repo.ClusterRepo
	creds    *repo.CredentialRepo
	obj      *repo.TiDBObjRepo
	cipher   *crypto.Cipher
	mode     *ModeManager
	engine   *job.Engine
}

// NewTiDBObjService 构造服务。
func NewTiDBObjService(clusters *repo.ClusterRepo, creds *repo.CredentialRepo, obj *repo.TiDBObjRepo, cipher *crypto.Cipher, mode *ModeManager) *TiDBObjService {
	return &TiDBObjService{clusters: clusters, creds: creds, obj: obj, cipher: cipher, mode: mode}
}

// SetEngine 注入任务引擎。
func (s *TiDBObjService) SetEngine(e *job.Engine) { s.engine = e }

// EngineBound 报告任务引擎是否已注入。
func (s *TiDBObjService) EngineBound() bool { return s.engine != nil }

// submit 提交异步任务。
//
// engine 由装配期注入（SetEngine）。这里显式判空：漏注入时给出可读错误，
// 而不是在 engine 上做 nil 解引用直接 panic（历史上 Kill 会话因此报 500）。
func (s *TiDBObjService) submit(ctx context.Context, j *model.Job) (int64, error) {
	if s.engine == nil {
		return 0, apperr.Internal("任务引擎未初始化")
	}
	return s.engine.Submit(ctx, j)
}

// target 构造采集目标。
func (s *TiDBObjService) target(ctx context.Context, clusterID int64) (*model.Cluster, *collector.Target, error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, nil, apperr.NotFound("集群不存在")
		}
		return nil, nil, apperr.Internal("查询集群失败")
	}
	cred, cerr := s.creds.GetByClusterID(ctx, clusterID)
	if cerr != nil && !errors.Is(cerr, repo.ErrNotFound) {
		return nil, nil, apperr.Internal("读取凭据失败")
	}
	return c, collector.NewTarget(c, cred, s.cipher), nil
}

// ensureRGSupported 版本能力校验：不支持时返回 40901（D-6）。
func (s *TiDBObjService) ensureRGSupported(c *model.Cluster) error {
	caps := collector.BuildCapabilities(c.TiDBVersion, nil)
	if !caps.SupportsResourceGroup {
		return apperr.Conflict(collector.UnsupportedMessage("Resource Group", c.TiDBVersion, 7, 5))
	}
	return nil
}

// ListUsers 分页查询 TiDB 用户快照。
func (s *TiDBObjService) ListUsers(ctx context.Context, clusterID int64, f repo.TiDBUserFilter) (*repo.ListResult[model.TiDBUser], error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	list, total, lerr := s.obj.ListUsers(ctx, clusterID, f)
	if lerr != nil {
		return nil, apperr.Internal("查询 TiDB 用户失败")
	}
	// v7.5 以下 RG 列不适用，清空 resourceGroup 便于前端渲染「不适用」。
	if !collector.BuildCapabilities(c.TiDBVersion, nil).SupportsResourceGroup {
		for i := range list {
			list[i].ResourceGroup = ""
		}
	}
	res := repo.NewListResult(list, total, f.Page)
	return &res, nil
}

// CreateTiDBUserInput 创建 TiDB 用户入参。
type CreateTiDBUserInput struct {
	UserName      string   `json:"userName"`
	Host          string   `json:"host"`
	Password      string   `json:"password"`
	ResourceGroup string   `json:"resourceGroup"`
	Grants        []string `json:"grants"`
}

// CreateUser 创建 TiDB 用户（异步任务）。
func (s *TiDBObjService) CreateUser(ctx context.Context, clusterID int64, in CreateTiDBUserInput, operator *model.PlatformUser) (int64, error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return 0, apperr.NotFound("集群不存在")
	}
	if _, err := sqlsafe.Ident(in.UserName); err != nil {
		return 0, apperr.BadRequest("用户名不合法：仅允许字母、数字、下划线，1~64 位")
	}
	host := strings.TrimSpace(in.Host)
	if host == "" {
		host = "%"
	}
	if _, err := sqlsafe.Host(host); err != nil {
		return 0, apperr.BadRequest("Host 不合法")
	}
	if len(in.Password) < 8 {
		return 0, apperr.BadRequest("密码长度需不少于 8 位")
	}
	if n, cerr := s.obj.CountUsersByName(ctx, clusterID, in.UserName, host); cerr == nil && n > 0 {
		return 0, apperr.Conflict(fmt.Sprintf("用户 %s@%s 已存在", in.UserName, host))
	}
	for _, g := range in.Grants {
		if _, err := sqlsafe.Privilege(g); err != nil {
			return 0, apperr.BadRequest("授权项不合法：" + g)
		}
	}
	if in.ResourceGroup != "" {
		if err := s.ensureRGSupported(c); err != nil {
			return 0, err
		}
		if _, err := sqlsafe.Ident(in.ResourceGroup); err != nil {
			return 0, apperr.BadRequest("Resource Group 名称不合法")
		}
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	jobID, jerr := s.submit(ctx, &model.Job{
		Type:          job.TypeCreateTiDBUser,
		ClusterID:     &clusterID,
		TargetType:    "tidb_user",
		TargetLabel:   in.UserName + "@" + host,
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload: job.Marshal(job.CreateUserPayload{
			ClusterID:     clusterID,
			UserName:      in.UserName,
			Host:          host,
			Password:      in.Password,
			ResourceGroup: in.ResourceGroup,
			Grants:        in.Grants,
		}),
	})
	if jerr != nil {
		return 0, jerr
	}
	return jobID, nil
}

// DeleteUser 删除 TiDB 用户（异步任务）。
func (s *TiDBObjService) DeleteUser(ctx context.Context, clusterID, snapshotID int64, operator *model.PlatformUser) (int64, error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return 0, apperr.NotFound("集群不存在")
	}
	u, err := s.obj.GetUserByID(ctx, clusterID, snapshotID)
	if err != nil {
		return 0, apperr.NotFound("TiDB 用户不存在")
	}
	if strings.EqualFold(u.UserName, "root") {
		return 0, apperr.Forbidden("root 为受保护账号，不允许删除")
	}
	if strings.EqualFold(u.UserName, c.DBUser) {
		return 0, apperr.Forbidden("该账号为平台当前连接账号，不允许删除")
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	jobID, jerr := s.submit(ctx, &model.Job{
		Type:          job.TypeDropTiDBUser,
		ClusterID:     &clusterID,
		TargetType:    "tidb_user",
		TargetLabel:   u.UserName + "@" + u.Host,
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload: job.Marshal(job.DropUserPayload{
			ClusterID:    clusterID,
			SnapshotID:   snapshotID,
			UserName:     u.UserName,
			Host:         u.Host,
			OperatorName: operatorName(operator),
		}),
	})
	if jerr != nil {
		return 0, jerr
	}
	return jobID, nil
}

// ResetPassword 重置 TiDB 用户密码（异步任务）。
func (s *TiDBObjService) ResetPassword(ctx context.Context, clusterID, snapshotID int64, password string, operator *model.PlatformUser) (int64, error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return 0, apperr.NotFound("集群不存在")
	}
	u, err := s.obj.GetUserByID(ctx, clusterID, snapshotID)
	if err != nil {
		return 0, apperr.NotFound("TiDB 用户不存在")
	}
	if len(password) < 8 {
		return 0, apperr.BadRequest("新密码长度需不少于 8 位")
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	jobID, jerr := s.submit(ctx, &model.Job{
		Type:          job.TypeResetTiDBPassword,
		ClusterID:     &clusterID,
		TargetType:    "tidb_user",
		TargetLabel:   u.UserName + "@" + u.Host,
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload: job.Marshal(job.ResetPasswordPayload{
			ClusterID:  clusterID,
			SnapshotID: snapshotID,
			UserName:   u.UserName,
			Host:       u.Host,
			Password:   password,
		}),
	})
	if jerr != nil {
		return 0, jerr
	}
	return jobID, nil
}

// Grants 查询指定用户的授权（实时查询 SHOW GRANTS）。
func (s *TiDBObjService) Grants(ctx context.Context, clusterID, snapshotID int64) ([]string, error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	u, err := s.obj.GetUserByID(ctx, clusterID, snapshotID)
	if err != nil {
		return nil, apperr.NotFound("TiDB 用户不存在")
	}
	_, target, terr := s.target(ctx, clusterID)
	if terr != nil {
		return nil, terr
	}
	ident, ierr := sqlsafe.Ident(u.UserName)
	if ierr != nil {
		return nil, apperr.BadRequest("用户名包含非法字符")
	}
	hostLit, herr := sqlsafe.Host(u.Host)
	if herr != nil {
		return nil, apperr.BadRequest("Host 包含非法字符")
	}
	querier, ok := s.mode.Current().(collector.Querier)
	if !ok {
		return nil, apperr.Conflict("当前模式不支持实时授权查询")
	}
	rows, qerr := querier.Query(ctx, target, fmt.Sprintf("SHOW GRANTS FOR %s@%s", ident, hostLit))
	if qerr != nil {
		return nil, apperr.CollectFailed("查询授权失败: "+qerr.Error(), nil)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		for _, v := range r {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

// GrantInput 授权 / 回收入参。
type GrantInput struct {
	Scope string   `json:"scope"`
	DB    string   `json:"db"`
	Privs []string `json:"privs"`
}

// ChangeGrant 授予或回收权限（synchronous，执行后返回最新 grants 供复核差集）。
func (s *TiDBObjService) ChangeGrant(ctx context.Context, clusterID, snapshotID int64, in GrantInput, grant bool) ([]string, error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	u, err := s.obj.GetUserByID(ctx, clusterID, snapshotID)
	if err != nil {
		return nil, apperr.NotFound("TiDB 用户不存在")
	}
	if len(in.Privs) == 0 {
		return nil, apperr.BadRequest("请选择权限项")
	}
	privs := make([]string, 0, len(in.Privs))
	for _, p := range in.Privs {
		v, perr := sqlsafe.Privilege(p)
		if perr != nil {
			return nil, apperr.BadRequest("权限不合法：" + p)
		}
		privs = append(privs, v)
	}
	scope := "*.*"
	if in.Scope == "db" {
		if in.DB == "" {
			return nil, apperr.BadRequest("库级授权需指定数据库名")
		}
		dbIdent, derr := sqlsafe.Ident(in.DB)
		if derr != nil {
			return nil, apperr.BadRequest("数据库名不合法")
		}
		scope = dbIdent + ".*"
	}
	ident, ierr := sqlsafe.Ident(u.UserName)
	if ierr != nil {
		return nil, apperr.BadRequest("用户名包含非法字符")
	}
	hostLit, herr := sqlsafe.Host(u.Host)
	if herr != nil {
		return nil, apperr.BadRequest("Host 包含非法字符")
	}
	_, target, terr := s.target(ctx, clusterID)
	if terr != nil {
		return nil, terr
	}
	verb := "GRANT"
	if !grant {
		verb = "REVOKE"
	}
	stmt := fmt.Sprintf("%s %s ON %s %s %s@%s", verb, strings.Join(privs, ", "), scope,
		map[bool]string{true: "TO", false: "FROM"}[grant], ident, hostLit)
	if xerr := s.mode.Current().Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
		return nil, apperr.CollectFailed("执行授权变更失败: "+xerr.Error(), nil)
	}
	// 执行后复核。
	return s.Grants(ctx, clusterID, snapshotID)
}

// ListResourceGroups 查询 Resource Group（v7.5 以下 40901）。
func (s *TiDBObjService) ListResourceGroups(ctx context.Context, clusterID int64) ([]model.ResourceGroup, error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	if err := s.ensureRGSupported(c); err != nil {
		return nil, err
	}
	list, lerr := s.obj.ListResourceGroups(ctx, clusterID)
	if lerr != nil {
		return nil, apperr.Internal("查询 Resource Group 失败")
	}
	if list == nil {
		list = []model.ResourceGroup{}
	}
	for i := range list {
		if n, cerr := s.obj.CountUsersByResourceGroup(ctx, clusterID, list[i].Name); cerr == nil {
			list[i].UserCount = int(n)
		}
	}
	return list, nil
}

// RefreshResourceGroups 实时查询 information_schema.resource_groups 并写回快照。
//
// Resource Group 是会被平台新建 / 修改 / 删除的对象，快照表只反映「上次采集」那一刻；
// 「刷新」走这条路径：直接连库读当前 resource_groups，再把结果写回 resource_groups 表，
// 使新建 / 修改 / 删除后的最新状态立即可见。
func (s *TiDBObjService) RefreshResourceGroups(ctx context.Context, clusterID int64) ([]model.ResourceGroup, error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	if err := s.ensureRGSupported(c); err != nil {
		return nil, err
	}
	if s.mode.IsMock() {
		// 演示模式没有真实数据库可连，回放快照，避免页面直接报错。
		return s.ListResourceGroups(ctx, clusterID)
	}
	_, t, err := s.target(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, collector.ChannelTimeout)
	defer cancel()
	rows, ferr := collector.NewMySQLChannel(collector.ChannelTimeout).FetchResourceGroups(cctx, t)
	if ferr != nil {
		return nil, apperr.CollectFailed("实时查询 Resource Group 失败", map[string]any{
			"reason": firstLine(ferr.Error()),
		})
	}

	now := time.Now()
	list := make([]model.ResourceGroup, 0, len(rows))
	for _, r := range rows {
		b := 0
		if r.Burstable {
			b = 1
		}
		list = append(list, model.ResourceGroup{
			ClusterID: clusterID,
			Name:      r.Name,
			RUPerSec:  r.RUPerSec,
			Priority:  r.Priority,
			Burstable: b,
			UserCount: r.UserCount,
			Status:    r.Status,
			FetchedAt: now,
			CreatedAt: now,
		})
	}
	// 写回快照（尽力而为）：失败不影响本次返回，下次采集会覆盖。
	_ = s.obj.ReplaceResourceGroups(ctx, clusterID, list)
	return list, nil
}

// RGPriority 允许的 Resource Group 绝对优先级枚举。
var rgPriorities = map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true}

// rgName 校验 Resource Group 名称并返回反引号包裹的安全形式。
func rgName(name string) (string, error) {
	if name == "" {
		return "", apperr.BadRequest("Resource Group 名称不能为空")
	}
	q, err := sqlsafe.Ident(name)
	if err != nil {
		return "", apperr.BadRequest("Resource Group 名称不合法：仅允许字母、数字、下划线，1~64 位")
	}
	return q, nil
}

// rgRuPerSec 校验 RU_PER_SEC（非负整数，0 表示不限速）。
func rgRuPerSec(v string) (string, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return "", apperr.BadRequest("RU_PER_SEC 不能为空")
	}
	if !isNonNegInt(s) {
		return "", apperr.BadRequest("RU_PER_SEC 必须为非负整数")
	}
	return s, nil
}

// isNonNegInt 判断字符串是否为非负整数。
func isNonNegInt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// rgClause 构造 RU_PER_SEC / PRIORITY / BURSTABLE 的 SQL 片段。
//
// 名称已通过 sqlsafe.Ident 校验为反引号包裹形式；RU_PER_SEC 与 PRIORITY 均经过
// 白名单校验，BURSTABLE 为固定关键字，不拼接任何用户自由文本。
//
// alter 区分场景：CREATE 时 burstable=false 表示「不开启」直接省略关键字；
// ALTER 时 burstable=false 必须显式输出 BURSTABLE = FALSE，否则增量语义会
// 保持原值不变（导致「关闭」无效）。两者均为白名单固定文本，无注入风险。
func rgClause(name, ruPerSec, priority string, burstable bool, alter bool) (string, error) {
	ident, err := rgName(name)
	if err != nil {
		return "", err
	}
	ru, err := rgRuPerSec(ruPerSec)
	if err != nil {
		return "", err
	}
	sb := strings.Builder{}
	sb.WriteString(ident)
	sb.WriteString(" RU_PER_SEC = ")
	sb.WriteString(ru)
	if p := strings.ToUpper(strings.TrimSpace(priority)); p != "" {
		if !rgPriorities[p] {
			return "", apperr.BadRequest("PRIORITY 仅支持 LOW / MEDIUM / HIGH")
		}
		sb.WriteString(" PRIORITY = ")
		sb.WriteString(p)
	}
	if burstable {
		sb.WriteString(" BURSTABLE")
	} else if alter {
		sb.WriteString(" BURSTABLE = FALSE")
	}
	return sb.String(), nil
}

// CreateResourceGroupInput 新建 Resource Group 入参。
type CreateResourceGroupInput struct {
	Name      string `json:"name"`
	RUPerSec  string `json:"ruPerSec"`
	Priority  string `json:"priority"`
	Burstable bool   `json:"burstable"`
}

// AlterResourceGroupInput 修改 Resource Group 入参。
type AlterResourceGroupInput struct {
	RUPerSec  string `json:"ruPerSec"`
	Priority  string `json:"priority"`
	Burstable bool   `json:"burstable"`
}

// CreateResourceGroup 新建 Resource Group（同步执行）。
func (s *TiDBObjService) CreateResourceGroup(ctx context.Context, clusterID int64, in CreateResourceGroupInput) error {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return apperr.NotFound("集群不存在")
	}
	if err := s.ensureRGSupported(c); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(in.Name), "default") {
		return apperr.Conflict("default 为系统内置资源组，无法重复创建")
	}
	clause, err := rgClause(in.Name, in.RUPerSec, in.Priority, in.Burstable, false)
	if err != nil {
		return err
	}
	_, target, terr := s.target(ctx, clusterID)
	if terr != nil {
		return terr
	}
	stmt := fmt.Sprintf("CREATE RESOURCE GROUP %s", clause)
	if xerr := s.mode.Current().Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
		return apperr.CollectFailed("创建 Resource Group 失败: "+xerr.Error(), nil)
	}
	return nil
}

// AlterResourceGroup 修改 Resource Group（同步执行，仅支持 RU_PER_SEC / PRIORITY / BURSTABLE）。
func (s *TiDBObjService) AlterResourceGroup(ctx context.Context, clusterID int64, name string, in AlterResourceGroupInput) error {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return apperr.NotFound("集群不存在")
	}
	if err := s.ensureRGSupported(c); err != nil {
		return err
	}
	clause, err := rgClause(name, in.RUPerSec, in.Priority, in.Burstable, true)
	if err != nil {
		return err
	}
	_, target, terr := s.target(ctx, clusterID)
	if terr != nil {
		return terr
	}
	stmt := fmt.Sprintf("ALTER RESOURCE GROUP %s", clause)
	if xerr := s.mode.Current().Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
		return apperr.CollectFailed("修改 Resource Group 失败: "+xerr.Error(), nil)
	}
	return nil
}

// DeleteResourceGroup 删除 Resource Group（同步执行，default 受保护）。
func (s *TiDBObjService) DeleteResourceGroup(ctx context.Context, clusterID int64, name string) error {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return apperr.NotFound("集群不存在")
	}
	if err := s.ensureRGSupported(c); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(name), "default") {
		return apperr.Forbidden("default 为系统内置资源组，不允许删除")
	}
	ident, err := rgName(name)
	if err != nil {
		return err
	}
	_, target, terr := s.target(ctx, clusterID)
	if terr != nil {
		return terr
	}
	stmt := fmt.Sprintf("DROP RESOURCE GROUP %s", ident)
	if xerr := s.mode.Current().Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
		return apperr.CollectFailed("删除 Resource Group 失败: "+xerr.Error(), nil)
	}
	return nil
}

// BindRGInput RG 绑定 / 解绑入参。
type BindRGInput struct {
	UserName      string `json:"userName"`
	Host          string `json:"host"`
	ResourceGroup string `json:"resourceGroup"`
}

// BindResourceGroup 绑定 / 解绑用户的 Resource Group。
func (s *TiDBObjService) BindResourceGroup(ctx context.Context, clusterID int64, in BindRGInput, bind bool) error {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return apperr.NotFound("集群不存在")
	}
	if err := s.ensureRGSupported(c); err != nil {
		return err
	}
	rg := in.ResourceGroup
	if bind && rg == "" {
		return apperr.BadRequest("请选择 Resource Group")
	}
	if !bind {
		rg = "default"
	} else if _, err := sqlsafe.Ident(rg); err != nil {
		return apperr.BadRequest("Resource Group 名称不合法")
	}
	ident, ierr := sqlsafe.Ident(in.UserName)
	if ierr != nil {
		return apperr.BadRequest("用户名不合法")
	}
	hostLit, herr := sqlsafe.Host(in.Host)
	if herr != nil {
		return apperr.BadRequest("Host 不合法")
	}
	_, target, terr := s.target(ctx, clusterID)
	if terr != nil {
		return terr
	}
	stmt := fmt.Sprintf("ALTER USER %s@%s RESOURCE GROUP %s", ident, hostLit, rg)
	if xerr := s.mode.Current().Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
		return apperr.CollectFailed("绑定 Resource Group 失败: "+xerr.Error(), nil)
	}
	if uerr := s.obj.UpdateUserResourceGroup(ctx, clusterID, in.UserName, in.Host, rg); uerr != nil {
		return apperr.Internal("更新用户快照失败")
	}
	return nil
}

// ListSessions 分页查询会话快照。
func (s *TiDBObjService) ListSessions(ctx context.Context, clusterID int64, f repo.SessionFilter) (*repo.ListResult[model.Session], error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	list, total, err := s.obj.ListSessions(ctx, clusterID, f)
	if err != nil {
		return nil, apperr.Internal("查询会话失败")
	}
	res := repo.NewListResult(list, total, f.Page)
	return &res, nil
}

// KillSessions 批量 Kill 会话（异步任务）。
func (s *TiDBObjService) KillSessions(ctx context.Context, clusterID int64, sessionIDs []int64, operator *model.PlatformUser) (int64, error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return 0, apperr.NotFound("集群不存在")
	}
	if len(sessionIDs) == 0 {
		return 0, apperr.BadRequest("请选择要 Kill 的会话")
	}
	if len(sessionIDs) > 50 {
		return 0, apperr.BadRequest("单次最多 Kill 50 个会话")
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	jobID, err := s.submit(ctx, &model.Job{
		Type:          job.TypeKillSession,
		ClusterID:     &clusterID,
		TargetType:    "session",
		TargetLabel:   fmt.Sprintf("%d 个会话", len(sessionIDs)),
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload:       job.Marshal(job.KillSessionPayload{ClusterID: clusterID, SessionIDs: sessionIDs}),
	})
	if err != nil {
		return 0, err
	}
	return jobID, nil
}

// SessionsResult 会话列表结果（快照或实时同构）。
type SessionsResult struct {
	List      []model.Session `json:"list"`
	Total     int64           `json:"total"`
	Page      int             `json:"page"`
	PageSize  int             `json:"pageSize"`
	Live      bool            `json:"live"`
	Source    string          `json:"source"`
	FetchedAt time.Time       `json:"fetchedAt"`
}

// RefreshSessions 实时查询 information_schema.cluster_processlist。
//
// 会话天然是瞬时数据，快照只能代表上次采集那一刻，因此「刷新」走这条路径：
// 直接连库读全集群 sessions，过滤/排序/分页在内存完成，并顺手把结果写回
// sessions 表（Kill 等后续动作仍按快照表取会话，不必再查一次库）。
func (s *TiDBObjService) RefreshSessions(ctx context.Context, clusterID int64, f repo.SessionFilter) (*SessionsResult, error) {
	if s.mode.IsMock() {
		// 演示模式没有真实数据库可连，回放快照，避免页面直接报错。
		snap, err := s.ListSessions(ctx, clusterID, f)
		if err != nil {
			return nil, err
		}
		return &SessionsResult{
			List: snap.List, Total: snap.Total, Page: snap.Page, PageSize: snap.PageSize,
			Source: "mock", FetchedAt: time.Now(),
		}, nil
	}
	_, t, err := s.target(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, collector.ChannelTimeout)
	defer cancel()
	rows, ferr := collector.NewMySQLChannel(collector.ChannelTimeout).FetchSessions(cctx, t)
	if ferr != nil {
		return nil, apperr.CollectFailed("实时查询会话失败", map[string]any{
			"reason": firstLine(ferr.Error()),
		})
	}

	now := time.Now()
	f.Page.Normalize()
	pageRows, total, all := buildSessionPage(clusterID, rows, f, now)

	// 写回快照（尽力而为）：写整份实时结果而不是当前页，否则翻页会把缓存覆盖成 20 条，
	// Kill 等按快照表取会话的动作就会找不到目标。失败不影响本次返回，下次采集会覆盖。
	_ = s.obj.ReplaceSessions(ctx, clusterID, all)

	return &SessionsResult{
		List: pageRows, Total: total, Page: f.Page.Page, PageSize: f.Page.PageSize,
		Live: true, Source: "cluster_processlist", FetchedAt: now,
	}, nil
}

// buildSessionPage 对实时结果做关键词过滤 → 排序 → 分页，
// 同时返回过滤后的全量列表（用于写回快照）。
//
// 抽成纯函数：集群级 processlist 通常只有几十行，内存处理比拼 SQL 简单，
// 也方便单测覆盖过滤与排序规则。
func buildSessionPage(clusterID int64, rows []collector.SessionRow, f repo.SessionFilter, now time.Time) ([]model.Session, int64, []model.Session) {
	f.Page.Normalize()
	kw := strings.ToLower(strings.TrimSpace(f.Keyword))
	filtered := make([]model.Session, 0, len(rows))
	for _, r := range rows {
		if kw != "" && !sessionMatches(r, kw) {
			continue
		}
		filtered = append(filtered, model.Session{
			ClusterID: clusterID,
			SessionID: r.ID,
			Instance:  r.Instance,
			UserName:  r.UserName,
			Host:      r.Host,
			DB:        r.DB,
			Command:   r.Command,
			TimeSec:   r.TimeSec,
			State:     r.State,
			SQLText:   r.SQLText,
			SQLDigest: r.SQLDigest,
			FetchedAt: now,
			CreatedAt: now,
		})
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if f.SortBy == "id" {
			return filtered[i].SessionID < filtered[j].SessionID
		}
		return filtered[i].TimeSec > filtered[j].TimeSec
	})

	total := int64(len(filtered))
	start := f.Page.Offset()
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + f.Page.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, filtered
}

// sessionMatches 会话关键词匹配：用户 / 来源 / 实例 / 库 / SQL 原文 / SQL 指纹。
func sessionMatches(r collector.SessionRow, kw string) bool {
	for _, s := range []string{r.UserName, r.Host, r.Instance, r.DB, r.SQLText, r.SQLDigest} {
		if s != "" && strings.Contains(strings.ToLower(s), kw) {
			return true
		}
	}
	return false
}

// firstLine 取错误首行并截断，避免把多行驱动错误整条抛给前端。
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// ListVariables 分页查询参数快照。
func (s *TiDBObjService) ListVariables(ctx context.Context, clusterID int64, f repo.VariableFilter) (*repo.ListResult[model.Variable], error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	list, total, err := s.obj.ListVariables(ctx, clusterID, f)
	if err != nil {
		return nil, apperr.Internal("查询参数失败")
	}
	res := repo.NewListResult(list, total, f.Page)
	return &res, nil
}

// VariablesResult 参数列表结果（快照或实时同构）。
type VariablesResult struct {
	List      []model.Variable `json:"list"`
	Total     int64            `json:"total"`
	Page      int              `json:"page"`
	PageSize  int              `json:"pageSize"`
	Live      bool             `json:"live"`
	Source    string           `json:"source"`
	FetchedAt time.Time        `json:"fetchedAt"`
}

// RefreshVariables 实时读取全局参数（SHOW GLOBAL VARIABLES）。
//
// 参数与集群配置一样会被人随时改：页面「刷新」走这条路径，直接连库取当前值，
// 过滤/排序/分页在内存完成，并顺手把结果写回 variables 表，
// 使「仅看差异」统计、CSV 导出等仍基于最新数据。
func (s *TiDBObjService) RefreshVariables(ctx context.Context, clusterID int64, f repo.VariableFilter) (*VariablesResult, error) {
	if s.mode.IsMock() {
		// 演示模式没有真实数据库可连，回放快照，避免页面直接报错。
		snap, err := s.ListVariables(ctx, clusterID, f)
		if err != nil {
			return nil, err
		}
		return &VariablesResult{
			List: snap.List, Total: snap.Total, Page: snap.Page, PageSize: snap.PageSize,
			Source: "mock", FetchedAt: time.Now(),
		}, nil
	}
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	_, t, err := s.target(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, collector.ChannelTimeout)
	defer cancel()
	rows, ferr := collector.NewMySQLChannel(collector.ChannelTimeout).FetchVariables(cctx, t)
	if ferr != nil {
		return nil, apperr.CollectFailed("实时查询参数失败", map[string]any{
			"reason": firstLine(ferr.Error()),
		})
	}

	now := time.Now()
	pageRows, total, all := buildVariablePage(clusterID, rows, f, now)

	// 写回快照（尽力而为）：写整份实时结果而不是当前页，否则翻页会把缓存覆盖成 20 条。
	_ = s.obj.ReplaceVariables(ctx, clusterID, all)

	return &VariablesResult{
		List: pageRows, Total: total, Page: f.Page.Page, PageSize: f.Page.PageSize,
		Live: true, Source: "show_global_variables", FetchedAt: now,
	}, nil
}

// buildVariablePage 对实时结果做过滤 → 排序 → 分页，并返回过滤后的全量列表（写回快照用）。
//
// 过滤口径与快照查询保持一致：关键词匹配参数名或值，onlyDiff 只留与默认值不同的项。
func buildVariablePage(clusterID int64, rows []collector.VariableRow, f repo.VariableFilter, now time.Time) ([]model.Variable, int64, []model.Variable) {
	f.Page.Normalize()
	kw := strings.ToLower(strings.TrimSpace(f.Keyword))
	filtered := make([]model.Variable, 0, len(rows))
	for _, r := range rows {
		if kw != "" &&
			!strings.Contains(strings.ToLower(r.Name), kw) &&
			!strings.Contains(strings.ToLower(r.Value), kw) {
			continue
		}
		differs := 0
		if r.DefaultValue != "" && r.Value != r.DefaultValue {
			differs = 1
		}
		if f.OnlyDiff && differs == 0 {
			continue
		}
		dynamic := 0
		if r.IsDynamic {
			dynamic = 1
		}
		filtered = append(filtered, model.Variable{
			ClusterID:    clusterID,
			Name:         r.Name,
			Value:        r.Value,
			DefaultValue: r.DefaultValue,
			Scope:        r.Scope,
			IsDynamic:    dynamic,
			Description:  r.Description,
			Differs:      differs,
			FetchedAt:    now,
			CreatedAt:    now,
		})
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })

	total := int64(len(filtered))
	start := f.Page.Offset()
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + f.Page.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, filtered
}

// ExportVariables 导出参数 CSV。
func (s *TiDBObjService) ExportVariables(ctx context.Context, clusterID int64, onlyDiff bool) ([]byte, string, error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return nil, "", apperr.NotFound("集群不存在")
	}
	list, err := s.obj.AllVariables(ctx, clusterID, onlyDiff)
	if err != nil {
		return nil, "", apperr.Internal("导出参数失败")
	}
	rows := make([][]string, 0, len(list))
	for _, v := range list {
		diff := "否"
		if v.Differs == 1 {
			diff = "是"
		}
		dynamic := "否"
		if v.IsDynamic == 1 {
			dynamic = "是"
		}
		rows = append(rows, []string{v.Name, v.Value, v.DefaultValue, v.Scope, dynamic, diff})
	}
	data, cerr := buildCSV([]string{"变量名", "当前值", "默认值", "范围", "动态生效", "与默认值不同"}, rows)
	if cerr != nil {
		return nil, "", apperr.Internal("生成 CSV 失败")
	}
	return data, csvFilename("variables_" + c.Name), nil
}

// UpdateVariable 修改全局变量（严格白名单，D-1）。
func (s *TiDBObjService) UpdateVariable(ctx context.Context, clusterID int64, name, value string) error {
	if !variableWhitelist[name] {
		return apperr.Forbidden("该参数不在允许修改的白名单内")
	}
	if strings.ContainsAny(value, ";'\"\\") {
		return apperr.BadRequest("参数值包含非法字符")
	}
	_, target, err := s.target(ctx, clusterID)
	if err != nil {
		return err
	}
	ident, ierr := sqlsafe.Ident(strings.ReplaceAll(name, "-", "_"))
	if ierr != nil {
		return apperr.BadRequest("参数名不合法")
	}
	stmt := fmt.Sprintf("SET GLOBAL %s = %s", ident, quoteValue(value))
	if xerr := s.mode.Current().Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
		return apperr.CollectFailed("修改参数失败: "+xerr.Error(), nil)
	}
	// 局部刷新快照。
	if existing, gerr := s.obj.GetVariableByName(ctx, clusterID, name); gerr == nil {
		differs := existing.DefaultValue != "" && value != existing.DefaultValue
		_ = s.obj.UpdateVariableValue(ctx, clusterID, name, value, differs)
	}
	return nil
}

// quoteValue 判断参数值形态并加引号。
func quoteValue(v string) string {
	if v == "" {
		return "''"
	}
	numeric := true
	for i, r := range v {
		if r == '-' && i == 0 {
			continue
		}
		if r < '0' || r > '9' {
			if r != '.' {
				numeric = false
				break
			}
		}
	}
	if numeric {
		return v
	}
	if strings.EqualFold(v, "on") || strings.EqualFold(v, "off") {
		return "'" + strings.ToLower(v) + "'"
	}
	return "'" + v + "'"
}
