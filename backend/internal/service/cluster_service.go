package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/addrmap"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// mockClusterNames 演示模式内置集群名。
var mockClusterNames = collector.MockClusterNames

// ClusterService 集群领域服务。
type ClusterService struct {
	clusters    *repo.ClusterRepo
	instances   *repo.InstanceRepo
	hosts       *repo.HostRepo
	creds       *repo.CredentialRepo
	jobs        *repo.JobRepo
	audits      *repo.AuditRepo
	settings    *SettingService
	mode        *ModeManager
	cipher      *crypto.Cipher
	engine      *job.Engine
	topoRepo    *repo.TiDBObjRepo
	probeTokens *ProbeTokenSigner
	// addrMaps 地址映射规则仓储：多网络平面下把内网地址改写为 TMP 可达地址。
	// 允许为 nil（单测/未接入场景），此时所有地址原样输出。
	addrMaps *repo.AddrMapRepo
}

// ClusterDeps 集群服务依赖集合。
type ClusterDeps struct {
	Clusters  *repo.ClusterRepo
	Instances *repo.InstanceRepo
	Hosts     *repo.HostRepo
	Creds     *repo.CredentialRepo
	Jobs      *repo.JobRepo
	Audits    *repo.AuditRepo
	Settings  *SettingService
	Mode      *ModeManager
	Cipher    *crypto.Cipher
	TiDBObj   *repo.TiDBObjRepo
	AddrMaps  *repo.AddrMapRepo
	// ProbeTokens 连接测试凭证签发器；为 nil 时使用进程内随机密钥（仅测试场景）。
	ProbeTokens *ProbeTokenSigner
}

// NewClusterService 构造集群服务。
func NewClusterService(d ClusterDeps) *ClusterService {
	signer := d.ProbeTokens
	if signer == nil {
		signer = NewProbeTokenSigner("tmp/insecure-default")
	}
	return &ClusterService{
		clusters:    d.Clusters,
		instances:   d.Instances,
		hosts:       d.Hosts,
		creds:       d.Creds,
		jobs:        d.Jobs,
		audits:      d.Audits,
		settings:    d.Settings,
		mode:        d.Mode,
		cipher:      d.Cipher,
		topoRepo:    d.TiDBObj,
		probeTokens: signer,
		addrMaps:    d.AddrMaps,
	}
}

// addrMapper 构建集群的地址映射器。
//
// 返回 nil 表示「不做改写」：未配置规则、仓储未注入或查询失败都走这条路径。
// 映射只影响 TMP 视角的访问，读失败不应该让集群详情整体报错。
func (s *ClusterService) addrMapper(ctx context.Context, clusterID int64) *addrmap.Mapper {
	if s == nil || s.addrMaps == nil {
		return nil
	}
	list, err := s.addrMaps.ListEnabled(ctx, clusterID)
	if err != nil {
		return nil
	}
	return BuildAddrMapper(list)
}

// SetEngine 注入任务引擎（解决构造期循环依赖）。
func (s *ClusterService) SetEngine(e *job.Engine) { s.engine = e }

// EngineBound 报告任务引擎是否已注入。
func (s *ClusterService) EngineBound() bool { return s.engine != nil }

// ---------------------------------------------------------------------------
// DTO
// ---------------------------------------------------------------------------

// ClusterBrief 集群列表行。
type ClusterBrief struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Draft             bool   `json:"draft"`
	Demo              bool   `json:"demo"`
	TiDBVersion       string `json:"tidbVersion"`
	DeployMode        string `json:"deployMode"`
	CPUArch           string `json:"cpuArch"`
	TiUPHost          string `json:"tiupHost"`
	TiUPPort          int    `json:"tiupPort"`
	TiUPUser          string `json:"tiupUser"`
	ComponentCount    int    `json:"componentCount"`
	HostCount         int    `json:"hostCount"`
	InstanceCount     int    `json:"instanceCount"`
	Status            string `json:"status"`
	CollectStatus     string `json:"collectStatus"`
	CollectFailStreak int    `json:"collectFailStreak"`
	Collecting        bool   `json:"collecting"`
	AlertCount        int    `json:"alertCount"`
	// AlertMaxLevel 最高告警级别（critical/warning/info），MVP 未接入告警时为空。
	AlertMaxLevel string `json:"alertMaxLevel"`
	// AbnormalInstanceCount 非 up 状态的实例数（down / unhealthy / unknown）。
	AbnormalInstanceCount int        `json:"abnormalInstanceCount"`
	LastCollectAt         *time.Time `json:"lastCollectAt"`
	LastSuccessAt         *time.Time `json:"lastSuccessAt"`
	FetchedAt             *time.Time `json:"fetchedAt"`
	LastCollectError      string     `json:"lastCollectError"`
	// CollectSource 最近一次采集的元数据来源（display_json / display_text / meta_yaml）。
	CollectSource string `json:"collectSource"`
	// CollectWarnings 最近一次采集的交叉校验告警（TiUP 报告状态与实测连通性矛盾）。
	CollectWarnings []string  `json:"collectWarnings"`
	CreatedAt       time.Time `json:"createdAt"`
}

// CredentialMask 凭据掩码视图。
type CredentialMask struct {
	SSHPassword string     `json:"sshPassword"`
	SSHKey      string     `json:"sshKey"`
	DBPassword  string     `json:"dbPassword"`
	UpdatedAt   *time.Time `json:"updatedAt"`
}

// ClusterDetail 集群详情。
type ClusterDetail struct {
	ClusterBrief
	TiUPDir          string                  `json:"tiupDir"`
	DBHost           string                  `json:"dbHost"`
	DBPort           int                     `json:"dbPort"`
	DBUser           string                  `json:"dbUser"`
	PDEndpoint       string                  `json:"pdEndpoint"`
	DashboardURLs    []string                `json:"dashboardUrls"`
	PrometheusURLs   []string                `json:"prometheusUrls"`
	GrafanaURLs      []string                `json:"grafanaUrls"`
	AlertmanagerURLs []string                `json:"alertmanagerUrls"`
	Credentials      CredentialMask          `json:"credentials"`
	Capabilities     *collector.Capabilities `json:"capabilities"`
	// AddrMapped 表示该集群配置了启用的地址映射规则，
	// 上面这些地址已按规则改写为 TMP 可达地址（原始值仍保存在采集快照里）。
	AddrMapped bool `json:"addrMapped"`
}

// ToBrief 转换为列表行。
func ToBrief(c *model.Cluster) ClusterBrief {
	return ClusterBrief{
		ID:                c.ID,
		Name:              c.Name,
		Draft:             c.Draft == 1,
		Demo:              c.IsDemo == 1,
		TiDBVersion:       c.TiDBVersion,
		DeployMode:        c.DeployMode,
		CPUArch:           c.CPUArch,
		TiUPHost:          c.TiUPHost,
		TiUPPort:          c.TiUPPort,
		TiUPUser:          c.TiUPUser,
		ComponentCount:    c.ComponentCount,
		HostCount:         c.HostCount,
		InstanceCount:     c.InstanceCount,
		Status:            c.Status,
		CollectStatus:     c.CollectStatus,
		CollectFailStreak: c.CollectFailStreak,
		Collecting:        c.Collecting == 1,
		AlertCount:        0, // MVP 未接入告警（Q5）
		LastCollectAt:     c.LastCollectAt,
		LastSuccessAt:     c.LastSuccessAt,
		FetchedAt:         c.FetchedAt,
		LastCollectError:  c.LastCollectError,
		CollectSource:     c.CollectSource,
		CollectWarnings:   parseStringList(c.CollectWarnings),
		CreatedAt:         c.CreatedAt,
	}
}

// parseURLList 解析 JSON 数组字符串为地址列表。
func parseURLList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// parseStringList 解析 JSON 字符串数组（采集告警等），空值或解析失败返回空切片。
func parseStringList(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// encodeURLList 把地址列表编码为 JSON 数组字符串。
func encodeURLList(list []string) string {
	if len(list) == 0 {
		return ""
	}
	b, err := json.Marshal(list)
	if err != nil {
		return ""
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// List 分页查询集群。
func (s *ClusterService) List(ctx context.Context, f repo.ClusterFilter) (*repo.ListResult[ClusterBrief], error) {
	f.IncludeDraft = true
	list, total, err := s.clusters.List(ctx, f)
	if err != nil {
		return nil, apperr.Internal("查询集群列表失败")
	}
	views := make([]ClusterBrief, 0, len(list))
	ids := make([]int64, 0, len(list))
	for i := range list {
		views = append(views, ToBrief(&list[i]))
		ids = append(ids, list[i].ID)
	}
	// 一次性聚合非 up 实例数，避免逐集群查询造成 N+1。
	if counts, cerr := s.clusters.AbnormalInstanceCounts(ctx, ids); cerr == nil {
		for i := range views {
			views[i].AbnormalInstanceCount = int(counts[views[i].ID])
		}
	}
	res := repo.NewListResult(views, total, f.Page)
	return &res, nil
}

// ClusterOption 下拉选项。
//
// TiUPHost 用于区分「同名不同中控机」的集群（多套集群可能同名）。
type ClusterOption struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	TiDBVersion string `json:"tidbVersion"`
	Draft       bool   `json:"draft"`
	TiUPHost    string `json:"tiupHost"`
}

// Options 下拉用精简列表。
func (s *ClusterService) Options(ctx context.Context) ([]ClusterOption, error) {
	list, err := s.clusters.Options(ctx)
	if err != nil {
		return nil, apperr.Internal("查询集群列表失败")
	}
	out := make([]ClusterOption, 0, len(list))
	for _, c := range list {
		out = append(out, ClusterOption{
			ID:          c.ID,
			Name:        c.Name,
			Status:      c.Status,
			TiDBVersion: c.TiDBVersion,
			Draft:       c.Draft == 1,
			TiUPHost:    c.TiUPHost,
		})
	}
	return out, nil
}

// Get 查询集群详情。
func (s *ClusterService) Get(ctx context.Context, id int64) (*ClusterDetail, error) {
	c, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.NotFound("集群不存在")
		}
		return nil, apperr.Internal("查询集群失败")
	}
	return s.toDetail(ctx, c)
}

// toDetail 组装详情（凭据一律掩码）。
//
// 地址类字段（PD / Dashboard / Grafana / Prometheus / Alertmanager）在这里做映射改写：
// 采集落库的值恒为集群内网平面的原始地址，映射只作用于「TMP 视角的读」。
// DBHost 与 TiUPHost 是用户录入的、本就要求可达的地址，不做改写 —— 否则会出现
// 「页面显示 A、实际连 B」的口径分裂。
func (s *ClusterService) toDetail(ctx context.Context, c *model.Cluster) (*ClusterDetail, error) {
	mp := s.addrMapper(ctx, c.ID)
	// mapped 只在「确实改写了某个地址」时为真：配了规则但一条都没命中时，
	// 页面不该显示「已映射」——那会让用户误以为生效了。
	mapped := false
	mapOne := func(v string) string {
		if strings.TrimSpace(v) == "" {
			return v
		}
		out := mp.MapString(v)
		if out != v {
			mapped = true
		}
		return out
	}
	mapList := func(list []string) []string {
		if len(list) == 0 {
			return list
		}
		out := make([]string, len(list))
		for i, v := range list {
			out[i] = mapOne(v)
		}
		return out
	}

	d := &ClusterDetail{
		ClusterBrief:     ToBrief(c),
		TiUPDir:          c.TiUPDir,
		DBHost:           c.DBHost,
		DBPort:           c.DBPort,
		DBUser:           c.DBUser,
		PDEndpoint:       mapOne(c.PDEndpoint),
		DashboardURLs:    mapList(parseURLList(c.DashboardURLs)),
		PrometheusURLs:   mapList(parseURLList(c.PrometheusURLs)),
		GrafanaURLs:      mapList(parseURLList(c.GrafanaURLs)),
		AlertmanagerURLs: mapList(parseURLList(c.AlertmanagerURLs)),
	}
	// 必须放在字面量之后：AddrMapped 依赖上面几次 mapXxx 调用累积出的结果。
	d.AddrMapped = mapped
	if cred, err := s.creds.GetByClusterID(ctx, c.ID); err == nil && cred != nil {
		updated := cred.UpdatedAt
		d.Credentials = CredentialMask{
			SSHPassword: crypto.Mask(cred.SSHPasswordEnc),
			SSHKey:      crypto.Mask(cred.SSHKeyEnc),
			DBPassword:  crypto.Mask(cred.DBPasswordEnc),
			UpdatedAt:   &updated,
		}
	}
	if c.Capabilities != "" {
		var caps collector.Capabilities
		if err := json.Unmarshal([]byte(c.Capabilities), &caps); err == nil {
			// 支持矩阵可能已更新（如新增 v7.1.8-5.x 回移分支），按当前规则校正快照。
			d.Capabilities = collector.NormalizeCapabilities(&caps, c.TiDBVersion)
		}
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// 连接测试
// ---------------------------------------------------------------------------

// TestConnectionInput 连接测试入参（等价接管表单）。
type TestConnectionInput struct {
	ClusterID   int64  `json:"clusterId"`
	Name        string `json:"name"`
	TiUPHost    string `json:"tiupHost"`
	TiUPPort    int    `json:"tiupPort"`
	TiUPUser    string `json:"tiupUser"`
	SSHPassword string `json:"sshPassword"`
	SSHKey      string `json:"sshKey"`
	TiUPDir     string `json:"tiupDir"`
	DBHost      string `json:"dbHost"`
	DBPort      int    `json:"dbPort"`
	DBUser      string `json:"dbUser"`
	DBPassword  string `json:"dbPassword"`
	PDEndpoint  string `json:"pdEndpoint"`
}

// testConnectionTimeout 连接测试整体超时（PRD R-06）。
const testConnectionTimeout = 15 * time.Second

// TestConnectionResult 连接测试结果：三项探测结果 + 后续接管所需的凭证。
//
// TestToken 为无状态 HMAC 凭证（10 分钟有效），POST /clusters 时按请求体重算比对。
type TestConnectionResult struct {
	Results   []collector.ProbeResult `json:"results"`
	TestToken string                  `json:"testToken"`
	// Discovered PD 地址自动发现结果（仅在表单未填 PD 地址时返回）。
	Discovered *collector.PDDiscovery `json:"discovered,omitempty"`
}

// targetFromInput 由接管表单构造采集目标（尚未解析 PD 时 PDHost/PDPort 为空）。
func targetFromInput(in TestConnectionInput, tiupPort, dbPort int) *collector.Target {
	pdHost, pdPort := collector.ParsePDEndpoint(in.PDEndpoint)
	return &collector.Target{
		ClusterName:  in.Name,
		TiUPHost:     strings.TrimSpace(in.TiUPHost),
		TiUPPort:     tiupPort,
		TiUPUser:     in.TiUPUser,
		TiUPPassword: in.SSHPassword,
		TiUPKey:      in.SSHKey,
		TiUPDir:      in.TiUPDir,
		DBHost:       strings.TrimSpace(in.DBHost),
		DBPort:       dbPort,
		DBUser:       in.DBUser,
		DBPassword:   in.DBPassword,
		PDHost:       pdHost,
		PDPort:       pdPort,
	}
}

// DiscoverPDResult 自动发现 PD 的结果（附带便于前端直接填入的主地址）。
type DiscoverPDResult struct {
	Endpoints []string `json:"endpoints"`
	Primary   string   `json:"primary"`
	Source    string   `json:"source"`
	Message   string   `json:"message"`
	Mock      bool     `json:"mock"`
}

// DiscoverPD 单独提供「自动获取 PD 地址」能力，供接管表单按钮调用。
//
// 与 TestConnection 内部的自动发现共用同一实现，避免两处逻辑漂移。
func (s *ClusterService) DiscoverPD(ctx context.Context, in TestConnectionInput) (*DiscoverPDResult, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, apperr.BadRequest("请先填写集群名称：TiUP 元数据按集群名定位")
	}
	tiupPort, dbPort, err := validateTakeoverAddresses(in.Name, in.TiUPHost, in.TiUPPort, in.DBHost, in.DBPort, "")
	if err != nil {
		return nil, err
	}
	target := targetFromInput(in, tiupPort, dbPort)
	disc, derr := s.discoverPD(ctx, target)
	if derr != nil {
		return nil, apperr.BadRequest(derr.Error())
	}
	return &DiscoverPDResult{
		Endpoints: disc.Endpoints,
		Primary:   disc.Primary(),
		Source:    disc.Source,
		Message:   disc.Message,
		Mock:      s.mode.IsMock(),
	}, nil
}

// discoverPD 调用当前采集模式的 PD 自动发现能力。
func (s *ClusterService) discoverPD(ctx context.Context, target *collector.Target) (*collector.PDDiscovery, error) {
	discoverer, ok := s.mode.Current().(collector.PDDiscoverer)
	if !ok {
		return nil, errors.New("当前采集模式不支持自动发现 PD 地址")
	}
	return discoverer.DiscoverPD(ctx, target)
}

// TestConnection 三通道并行探测，整体不超过 15s，三项独立返回成败与耗时。
func (s *ClusterService) TestConnection(ctx context.Context, in TestConnectionInput) (*TestConnectionResult, error) {
	if in.ClusterID > 0 {
		// 使用已保存凭据测试，未填写的字段从库中补齐。
		c, err := s.clusters.GetByID(ctx, in.ClusterID)
		if err != nil {
			return nil, apperr.NotFound("集群不存在")
		}
		cred, _ := s.creds.GetByClusterID(ctx, c.ID)
		target := collector.NewTarget(c, cred, s.cipher)
		if strings.TrimSpace(in.TiUPHost) != "" {
			if err := ValidateHostField("TiUP 主机", in.TiUPHost); err != nil {
				return nil, apperr.BadRequest(err.Error())
			}
			target.TiUPHost = strings.TrimSpace(in.TiUPHost)
		}
		if in.TiUPPort > 0 {
			if err := ValidatePort("SSH 端口", in.TiUPPort); err != nil {
				return nil, apperr.BadRequest(err.Error())
			}
			target.TiUPPort = in.TiUPPort
		}
		if in.TiUPUser != "" {
			target.TiUPUser = in.TiUPUser
		}
		if in.SSHPassword != "" {
			target.TiUPPassword = in.SSHPassword
		}
		if strings.TrimSpace(in.DBHost) != "" {
			if err := ValidateHostField("数据库地址", in.DBHost); err != nil {
				return nil, apperr.BadRequest(err.Error())
			}
			target.DBHost = strings.TrimSpace(in.DBHost)
		}
		if in.DBPort > 0 {
			if err := ValidatePort("数据库端口", in.DBPort); err != nil {
				return nil, apperr.BadRequest(err.Error())
			}
			target.DBPort = in.DBPort
		}
		if in.DBUser != "" {
			target.DBUser = in.DBUser
		}
		if in.DBPassword != "" {
			target.DBPassword = in.DBPassword
		}
		if strings.TrimSpace(in.PDEndpoint) != "" {
			if err := ValidatePDEndpoint(in.PDEndpoint); err != nil {
				return nil, apperr.BadRequest(err.Error())
			}
			h, p := collector.ParsePDEndpoint(in.PDEndpoint)
			target.PDHost, target.PDPort = h, p
		}
		results, err := s.probeScopes(ctx, target)
		if err != nil {
			return nil, err
		}
		sortProbeResults(results)
		// 已有集群走「编辑纳管」路径，不需要接管凭证。
		return &TestConnectionResult{Results: results}, nil
	}

	// 新建接管路径：先做格式校验，再探测，最后签发凭证。
	tiupPort, dbPort, err := validateTakeoverAddresses(in.Name, in.TiUPHost, in.TiUPPort, in.DBHost, in.DBPort, in.PDEndpoint)
	if err != nil {
		return nil, err
	}
	target := targetFromInput(in, tiupPort, dbPort)

	var discovered *collector.PDDiscovery
	var discoverErr error
	var results []collector.ProbeResult

	if strings.TrimSpace(in.PDEndpoint) == "" {
		// PD 留空：自动发现与 SSH/MySQL 探测并行，避免两者超时串行叠加（PRD R-06 要求 15s 内返回）。
		type discOut struct {
			disc *collector.PDDiscovery
			err  error
		}
		discCh := make(chan discOut, 1)
		discCtx, discCancel := context.WithTimeout(ctx, pdDiscoveryTimeout)
		go func() {
			d, e := s.discoverPD(discCtx, target)
			discCh <- discOut{disc: d, err: e}
		}()

		results, err = s.probeScopes(ctx, target, collector.ScopeSSH, collector.ScopeMySQL)
		out := <-discCh
		discCancel()
		if err != nil {
			return nil, err
		}
		discovered, discoverErr = out.disc, out.err

		if discovered != nil {
			pdHost, pdPort := collector.ParsePDEndpoint(discovered.Primary())
			target.PDHost, target.PDPort = pdHost, pdPort
			if pdResults, perr := s.probeScopes(ctx, target, collector.ScopePD); perr == nil {
				results = append(results, pdResults...)
			}
		}
		if discovered == nil {
			// 未填 PD 且自动发现失败：明确标注「已跳过」，不伪装成 PD 故障。
			reason := "未填写 PD 地址，且未能自动识别"
			if discoverErr != nil {
				reason = discoverErr.Error()
			}
			results = append(results, collector.ProbeResult{
				Scope:   collector.ScopePD,
				OK:      false,
				Message: clipRunes(reason, 120) + "；PD 为建议项，不影响纳管",
				Detail:  map[string]any{"skipped": true},
			})
		}
	} else {
		results, err = s.probeScopes(ctx, target)
		if err != nil {
			return nil, err
		}
	}
	sortProbeResults(results)

	// 指纹始终使用表单原始值：留空的 PD 在测试与提交两侧都留空，避免凭证与表单错位。
	fingerprint := ProbeFingerprint(in.TiUPHost, in.TiUPUser, tiupPort, in.DBHost, dbPort, in.PDEndpoint)
	return &TestConnectionResult{
		Results:    results,
		TestToken:  s.probeTokens.Sign(fingerprint, time.Now()),
		Discovered: discovered,
	}, nil
}

// pdDiscoveryTimeout PD 自动发现超时（与探测并行执行，不叠加到 15s 总预算上）。
const pdDiscoveryTimeout = 8 * time.Second

// clipRunes 按字符截断字符串（超长时补省略号），用于收敛面向用户的错误文案。
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// validateTakeoverAddresses 统一校验接管表单的地址与端口，返回归一化后的端口。
//
// 校验顺序与前端表单一致，错误信息直接指明字段，便于定位。
func validateTakeoverAddresses(name, tiupHost string, tiupPort int, dbHost string, dbPort int, pdEndpoint string) (int, int, error) {
	if strings.TrimSpace(name) != "" {
		if err := ValidateClusterName(name); err != nil {
			return 0, 0, apperr.BadRequest(err.Error())
		}
	}
	if err := ValidateHostField("TiUP 主机", tiupHost); err != nil {
		return 0, 0, apperr.BadRequest(err.Error())
	}
	if err := ValidateHostField("数据库地址", dbHost); err != nil {
		return 0, 0, apperr.BadRequest(err.Error())
	}
	normalizedTiUPPort := tiupPort
	if normalizedTiUPPort == 0 {
		normalizedTiUPPort = 22
	}
	if err := ValidatePort("SSH 端口", normalizedTiUPPort); err != nil {
		return 0, 0, apperr.BadRequest(err.Error())
	}
	normalizedDBPort := dbPort
	if normalizedDBPort == 0 {
		normalizedDBPort = 4000
	}
	if err := ValidatePort("数据库端口", normalizedDBPort); err != nil {
		return 0, 0, apperr.BadRequest(err.Error())
	}
	// PD 地址可留空：留空时由服务端自动发现（TiUP 元数据 → cluster_info），
	// 发现失败也不阻塞纳管（PD 为建议通道）。手填时才做格式校验。
	if strings.TrimSpace(pdEndpoint) != "" {
		if err := ValidatePDEndpoint(pdEndpoint); err != nil {
			return 0, 0, apperr.BadRequest(err.Error())
		}
	}
	return normalizedTiUPPort, normalizedDBPort, nil
}

// probeScopes 执行指定通道的探测；不传 scopes 时探测全部三通道。
func (s *ClusterService) probeScopes(ctx context.Context, target *collector.Target, scopes ...collector.ProbeScope) ([]collector.ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, testConnectionTimeout)
	defer cancel()
	if len(scopes) == 0 {
		scopes = []collector.ProbeScope{collector.ScopeSSH, collector.ScopeMySQL, collector.ScopePD}
	}
	raw, err := s.mode.Current().Probe(ctx, target, scopes)
	if err != nil {
		return nil, apperr.CollectFailed("连接测试失败: "+err.Error(), nil)
	}
	// 采集器对未请求的通道会返回占位结果（"未执行该项探测"），此处按请求范围过滤，
	// 避免 PD 留空场景下出现两条 pd 结果。
	wanted := make(map[collector.ProbeScope]bool, len(scopes))
	for _, sc := range scopes {
		wanted[sc] = true
	}
	results := make([]collector.ProbeResult, 0, len(scopes))
	for _, r := range raw {
		if wanted[r.Scope] {
			results = append(results, r)
		}
	}
	return results, nil
}

// sortProbeResults 保证三项按 ssh / mysql / pd 固定顺序返回，前端逐项渲染。
func sortProbeResults(results []collector.ProbeResult) {
	order := map[collector.ProbeScope]int{collector.ScopeSSH: 0, collector.ScopeMySQL: 1, collector.ScopePD: 2}
	sort.SliceStable(results, func(i, j int) bool { return order[results[i].Scope] < order[results[j].Scope] })
}

// ---------------------------------------------------------------------------
// 接管 / 编辑 / 移除 / 刷新
// ---------------------------------------------------------------------------

// TakeoverInput 接管入参。
type TakeoverInput struct {
	Name        string `json:"name"`
	TiUPHost    string `json:"tiupHost"`
	TiUPPort    int    `json:"tiupPort"`
	TiUPUser    string `json:"tiupUser"`
	SSHPassword string `json:"sshPassword"`
	SSHKey      string `json:"sshKey"`
	TiUPDir     string `json:"tiupDir"`
	DBHost      string `json:"dbHost"`
	DBPort      int    `json:"dbPort"`
	DBUser      string `json:"dbUser"`
	DBPassword  string `json:"dbPassword"`
	PDEndpoint  string `json:"pdEndpoint"`
	// 访问入口（可选）：接管时手动补充，后续采集到快照会覆盖。
	DashboardURLs  []string `json:"dashboardUrls"`
	PrometheusURLs []string `json:"prometheusUrls"`
	GrafanaURLs    []string `json:"grafanaUrls"`
	SaveAsDraft    bool     `json:"saveAsDraft"`
	// TestToken 连接测试凭证，由 POST /clusters/test-connection 签发。
	TestToken string `json:"testToken"`
	// Force 仅 admin 可用：跳过连接测试凭证校验，审计中记录 force=true。
	Force bool `json:"force"`
}

// TakeoverResult 接管结果。
type TakeoverResult struct {
	ClusterID int64 `json:"clusterId"`
	JobID     int64 `json:"jobId"`
	Draft     bool  `json:"draft"`
	// Forced 本次是否为「跳过连接测试」的强制接管。
	Forced bool `json:"forced"`
	// Demo 演示模式接管：采集结果为内置样例，不代表真实集群。
	Demo bool `json:"demo"`
}

// duplicateClusterNameMsg 重复纳管的统一文案。
//
// 唯一键是 (tiup_host, name_lower)，因此提示必须点明「该中控机上」，
// 否则用户无法理解为什么别的中控机有同名集群却不算冲突。
func duplicateClusterNameMsg(name string) string {
	return fmt.Sprintf("该中控机上已存在同名集群「%s」（不区分大小写）", strings.TrimSpace(name))
}

// Takeover 接管集群：入参校验 → 连接测试凭证校验 → 落库 → 加密存凭据 → 提交异步接管采集任务。
func (s *ClusterService) Takeover(ctx context.Context, in TakeoverInput, operator *model.PlatformUser) (*TakeoverResult, error) {
	name := strings.TrimSpace(in.Name)
	if err := ValidateClusterName(name); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	tiupPort, dbPort, err := validateTakeoverAddresses(name, in.TiUPHost, in.TiUPPort, in.DBHost, in.DBPort, in.PDEndpoint)
	if err != nil {
		return nil, err
	}
	if err := ValidateURLList("Dashboard 地址", in.DashboardURLs); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	if err := ValidateURLList("Prometheus 地址", in.PrometheusURLs); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	if err := ValidateURLList("Grafana 地址", in.GrafanaURLs); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}

	// 连接测试凭证校验：草稿不采集，跳过；admin 可显式 force 跳过。
	forced := false
	if !in.SaveAsDraft {
		if terr := s.verifyProbeToken(in, tiupPort, dbPort); terr != nil {
			if !(in.Force && operator != nil && operator.Role == auth.RoleAdmin) {
				return nil, apperr.BadRequest("请先完成连接测试：" + terr.Error())
			}
			forced = true
		}
	}
	if forced {
		// 记录一次显式跳过的操作痕迹（service 层不直接写审计，由 handler 落库）。
		in.TestToken = ""
	}

	// PD 地址留空时自动发现并落库：TiUP 部署的集群 PD 拓扑一定在 meta.yaml 里，
	// 不该让用户抄一遍。发现失败也不阻塞纳管（PD 为建议通道）。
	pdEndpoint := strings.TrimSpace(in.PDEndpoint)
	if pdEndpoint == "" && !in.SaveAsDraft {
		target := targetFromInput(TestConnectionInput{
			Name:        name,
			TiUPHost:    in.TiUPHost,
			TiUPPort:    tiupPort,
			TiUPUser:    in.TiUPUser,
			SSHPassword: in.SSHPassword,
			SSHKey:      in.SSHKey,
			TiUPDir:     in.TiUPDir,
			DBHost:      in.DBHost,
			DBPort:      dbPort,
			DBUser:      in.DBUser,
			DBPassword:  in.DBPassword,
		}, tiupPort, dbPort)
		if disc, derr := s.discoverPD(ctx, target); derr == nil {
			pdEndpoint = disc.Primary()
		}
	}

	nameLower := strings.ToLower(name)
	tiupHost := strings.TrimSpace(in.TiUPHost)
	// 唯一键是「中控机 + 集群名」：不同中控机上允许同名集群（同批机器跑多套集群），
	// 只有同一台中控机上的同名集群才算重复纳管。
	if existing, err := s.clusters.GetByHostAndNameLower(ctx, tiupHost, nameLower); err == nil && existing != nil {
		return nil, apperr.BadRequest(duplicateClusterNameMsg(name))
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, apperr.Internal("校验集群名失败")
	}

	now := time.Now()
	var createdBy *int64
	if operator != nil {
		id := operator.ID
		createdBy = &id
	}
	draft := 0
	if in.SaveAsDraft {
		draft = 1
	}
	mockMode := s.mode != nil && s.mode.IsMock()
	isDemo := 0
	if mockMode {
		isDemo = 1
	}
	c := &model.Cluster{
		Name:           name,
		NameLower:      nameLower,
		Draft:          draft,
		IsDemo:         isDemo,
		DeployMode:     model.DeployModeUnknown,
		TiUPHost:       tiupHost,
		TiUPPort:       tiupPort,
		TiUPUser:       strings.TrimSpace(in.TiUPUser),
		TiUPDir:        strings.TrimSpace(in.TiUPDir),
		DBHost:         strings.TrimSpace(in.DBHost),
		DBPort:         dbPort,
		DBUser:         strings.TrimSpace(in.DBUser),
		PDEndpoint:     pdEndpoint,
		DashboardURLs:  encodeURLList(in.DashboardURLs),
		PrometheusURLs: encodeURLList(in.PrometheusURLs),
		GrafanaURLs:    encodeURLList(in.GrafanaURLs),
		Status:         model.ClusterStatusUnknown,
		CollectStatus:  model.ClusterStatusUnknown,
		CreatedBy:      createdBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.clusters.Create(ctx, c); err != nil {
		return nil, apperr.Internal("创建集群记录失败: " + err.Error())
	}

	// 凭据加密入库。
	if err := s.saveCredentials(ctx, c.ID, in.SSHPassword, in.SSHKey, in.DBPassword, createdBy); err != nil {
		return nil, err
	}

	res := &TakeoverResult{ClusterID: c.ID, Draft: in.SaveAsDraft, Forced: forced, Demo: mockMode}
	if in.SaveAsDraft {
		return res, nil
	}

	// 提交接管采集任务。
	jobID, err := s.engine.Submit(ctx, &model.Job{
		Type:          job.TypeTakeover,
		ClusterID:     &c.ID,
		TargetType:    "cluster",
		TargetLabel:   c.Name,
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload:       job.Marshal(job.TakeoverPayload{ClusterID: c.ID}),
	})
	if err != nil {
		return nil, err
	}
	_ = s.clusters.SetCollecting(ctx, c.ID, 1)
	res.JobID = jobID
	return res, nil
}

// verifyProbeToken 校验连接测试凭证是否与本次提交的连接参数一致。
func (s *ClusterService) verifyProbeToken(in TakeoverInput, tiupPort, dbPort int) error {
	token := strings.TrimSpace(in.TestToken)
	if token == "" {
		return errors.New("缺少连接测试凭证，请先测试连通性")
	}
	fingerprint := ProbeFingerprint(in.TiUPHost, in.TiUPUser, tiupPort, in.DBHost, dbPort, in.PDEndpoint)
	return s.probeTokens.Verify(fingerprint, token, time.Now())
}

// saveCredentials 加密并保存凭据。
func (s *ClusterService) saveCredentials(ctx context.Context, clusterID int64, sshPwd, sshKey, dbPwd string, by *int64) error {
	if sshPwd == "" && sshKey == "" && dbPwd == "" {
		return nil
	}
	encSSH, err := s.cipher.Encrypt(sshPwd)
	if err != nil {
		return apperr.Internal("OS 凭据加密失败")
	}
	encKey, err := s.cipher.Encrypt(sshKey)
	if err != nil {
		return apperr.Internal("SSH 私钥加密失败")
	}
	encDB, err := s.cipher.Encrypt(dbPwd)
	if err != nil {
		return apperr.Internal("数据库凭据加密失败")
	}
	cred := &model.ClusterCredential{
		ClusterID:      clusterID,
		SSHPasswordEnc: encSSH,
		SSHKeyEnc:      encKey,
		DBPasswordEnc:  encDB,
		KeyVersion:     1,
		UpdatedBy:      by,
	}
	if err := s.creds.Upsert(ctx, cred); err != nil {
		return apperr.Internal("保存凭据失败")
	}
	return nil
}

// UpdateInput 编辑纳管信息入参。
type UpdateInput struct {
	Name       string `json:"name"`
	TiUPHost   string `json:"tiupHost"`
	TiUPPort   int    `json:"tiupPort"`
	TiUPUser   string `json:"tiupUser"`
	TiUPDir    string `json:"tiupDir"`
	DBHost     string `json:"dbHost"`
	DBPort     int    `json:"dbPort"`
	DBUser     string `json:"dbUser"`
	PDEndpoint string `json:"pdEndpoint"`
}

// Update 编辑纳管信息；改后置 collect_status=degraded 提示需重采。
func (s *ClusterService) Update(ctx context.Context, id int64, in UpdateInput) (*ClusterDetail, error) {
	c, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	// 只校验本次提交的字段，未填写的沿用库内值。
	if strings.TrimSpace(in.TiUPHost) != "" {
		if err := ValidateHostField("TiUP 主机", in.TiUPHost); err != nil {
			return nil, apperr.BadRequest(err.Error())
		}
	}
	if strings.TrimSpace(in.DBHost) != "" {
		if err := ValidateHostField("数据库地址", in.DBHost); err != nil {
			return nil, apperr.BadRequest(err.Error())
		}
	}
	if in.TiUPPort != 0 {
		if err := ValidatePort("SSH 端口", in.TiUPPort); err != nil {
			return nil, apperr.BadRequest(err.Error())
		}
	}
	if in.DBPort != 0 {
		if err := ValidatePort("数据库端口", in.DBPort); err != nil {
			return nil, apperr.BadRequest(err.Error())
		}
	}
	if strings.TrimSpace(in.PDEndpoint) != "" {
		if err := ValidatePDEndpoint(in.PDEndpoint); err != nil {
			return nil, apperr.BadRequest(err.Error())
		}
	}
	if strings.TrimSpace(in.Name) != "" && strings.TrimSpace(in.Name) != c.Name {
		if err := ValidateClusterName(in.Name); err != nil {
			return nil, apperr.BadRequest(err.Error())
		}
	}
	fields := map[string]any{
		"updated_at":     time.Now(),
		"collect_status": model.ClusterStatusDegraded,
	}
	// 唯一性校验：业务唯一键是 (tiup_host, name_lower)，因此「改名」和「换中控机」
	// 都可能产生冲突，两者任一变化都要按**本次生效的**中控机 + 名字校验。
	newName := strings.TrimSpace(in.Name)
	newHost := strings.TrimSpace(in.TiUPHost)
	nameChanged := newName != "" && newName != c.Name
	hostChanged := newHost != "" && newHost != strings.TrimSpace(c.TiUPHost)
	if nameChanged || hostChanged {
		targetHost := strings.TrimSpace(c.TiUPHost)
		targetName := c.Name
		targetLower := c.NameLower
		if hostChanged {
			targetHost = newHost
		}
		if nameChanged {
			targetName = newName
			targetLower = strings.ToLower(newName)
		}
		if other, err := s.clusters.GetByHostAndNameLower(ctx, targetHost, targetLower); err == nil && other != nil && other.ID != c.ID {
			return nil, apperr.BadRequest(duplicateClusterNameMsg(targetName))
		}
		if nameChanged {
			fields["name"] = newName
			fields["name_lower"] = targetLower
		}
	}
	if in.TiUPHost != "" {
		fields["tiup_host"] = in.TiUPHost
	}
	if in.TiUPPort > 0 && in.TiUPPort <= 65535 {
		fields["tiup_port"] = in.TiUPPort
	}
	if in.TiUPUser != "" {
		fields["tiup_user"] = in.TiUPUser
	}
	if in.TiUPDir != "" {
		fields["tiup_dir"] = in.TiUPDir
	}
	if in.DBHost != "" {
		fields["db_host"] = in.DBHost
	}
	if in.DBPort > 0 && in.DBPort <= 65535 {
		fields["db_port"] = in.DBPort
	}
	if in.DBUser != "" {
		fields["db_user"] = in.DBUser
	}
	if in.PDEndpoint != "" {
		fields["pd_endpoint"] = in.PDEndpoint
	}
	if err := s.clusters.UpdateFields(ctx, id, fields); err != nil {
		return nil, apperr.Internal("更新集群失败")
	}
	updated, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		return nil, apperr.Internal("读取集群失败")
	}
	return s.toDetail(ctx, updated)
}

// Delete 移除纳管（软删 + 释放唯一键），不对真实集群做任何操作。
func (s *ClusterService) Delete(ctx context.Context, id int64, confirmName string) error {
	c, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		return apperr.NotFound("集群不存在")
	}
	if strings.TrimSpace(confirmName) != c.Name {
		return apperr.BadRequest("请输入完整集群名以确认移除")
	}
	if err := s.clusters.Remove(ctx, id, c.NameLower); err != nil {
		return apperr.Internal("移除纳管失败")
	}
	return nil
}

// RefreshResult 刷新采集结果。
type RefreshResult struct {
	ClusterID int64 `json:"clusterId"`
	JobID     int64 `json:"jobId"`
	Reused    bool  `json:"reused"`
	// Error 批量提交时单个集群的失败原因；单集群接口失败走 error 响应，不填该字段。
	// 批量接口整体仍返回 202，由前端按 jobId==0 + Error 展示哪些集群没提交成功。
	Error string `json:"error,omitempty"`
}

// Refresh 手动刷新采集；已有进行中任务则复用。
func (s *ClusterService) Refresh(ctx context.Context, id int64, operator *model.PlatformUser) (*RefreshResult, error) {
	c, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	for _, t := range []string{job.TypeCollect, job.TypeTakeover} {
		if existing, err := s.jobs.FindActiveByType(ctx, t, id); err == nil && existing != nil {
			_ = s.clusters.SetCollecting(ctx, id, 1)
			return &RefreshResult{ClusterID: id, JobID: existing.ID, Reused: true}, nil
		}
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	// 先占位，避免并发请求重复建任务。
	if err := s.clusters.SetCollecting(ctx, id, 1); err != nil {
		return nil, apperr.Internal("更新采集状态失败")
	}
	jobID, err := s.engine.Submit(ctx, &model.Job{
		Type:          job.TypeCollect,
		ClusterID:     &id,
		TargetType:    "cluster",
		TargetLabel:   c.Name,
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload:       job.Marshal(job.CollectPayload{ClusterID: id}),
	})
	if err != nil {
		_ = s.clusters.SetCollecting(ctx, id, 0)
		return nil, err
	}
	return &RefreshResult{ClusterID: id, JobID: jobID}, nil
}

// BatchRefresh 批量刷新。
func (s *ClusterService) BatchRefresh(ctx context.Context, ids []int64, operator *model.PlatformUser) ([]RefreshResult, error) {
	if len(ids) == 0 {
		return nil, apperr.BadRequest("请选择要刷新的集群")
	}
	if len(ids) > 50 {
		return nil, apperr.BadRequest("单次最多刷新 50 个集群")
	}
	out := make([]RefreshResult, 0, len(ids))
	for _, id := range ids {
		res, err := s.Refresh(ctx, id, operator)
		if err != nil {
			// 单个集群失败不拖垮整批：带上原因返回，前端据此提示「哪几个没提交成功」。
			out = append(out, RefreshResult{ClusterID: id, Error: failureReason(err)})
			continue
		}
		out = append(out, *res)
	}
	return out, nil
}

// failureReason 把批量提交的单条失败归一为可展示文案。
// 只透出领域错误（apperr）的 Message；其余（如 engine.Submit 透传的底层错误）
// 一律收敛为固定文案，避免 DB/网络细节漏到前端。
func failureReason(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	return "提交失败"
}

// operatorName 操作者展示名。
func operatorName(u *model.PlatformUser) string {
	if u == nil {
		return "system"
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// ---------------------------------------------------------------------------
// 快照落库（实现 job.SnapshotSink）
// ---------------------------------------------------------------------------

// PersistSnapshot 在同一事务内全量替换快照并推导健康状态。
//
// 该方法是 clusters.status / collect_status / collect_fail_streak 的唯一写入点。
func (s *ClusterService) PersistSnapshot(ctx context.Context, clusterID int64, snap *collector.Snapshot) error {
	if snap == nil {
		return errors.New("空快照")
	}
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("集群不存在: %w", err)
	}
	threshold := s.settings.ErrorStreak(ctx)
	everSucceeded := c.LastSuccessAt != nil
	status, collectStatus, streak := collector.Derive(snap.ChanErrors, c.CollectFailStreak, everSucceeded, threshold, snap.DownInstances())

	write := &repo.SnapshotWrite{
		ClusterID:     clusterID,
		Status:        status,
		CollectStatus: collectStatus,
		FailStreak:    streak,
		Success:       len(snap.ChanErrors) == 0 || status != model.ClusterStatusError,
		Source:        snap.Source,
		Warnings:      snap.Consistency,
		FetchedAt:     snap.FetchedAt,
	}
	if write.FetchedAt.IsZero() {
		write.FetchedAt = time.Now()
	}

	// PD 地址自愈：接管时既未填写也未自动发现的集群，采集到 PD 实例后回填，
	// 使后续 PD 通道（存储节点容量、副本模式）自动可用。
	// 仅在库内该字段为空时更新（repo 侧条件写），不会覆盖用户手填值。
	if strings.TrimSpace(c.PDEndpoint) == "" {
		for _, inst := range snap.Instances {
			if inst.Component == "pd" && inst.HostIP != "" && inst.Port > 0 {
				write.PDEndpoint = "http://" + net.JoinHostPort(inst.HostIP, strconv.Itoa(inst.Port))
				break
			}
		}
	}
	// 只有采集到数据才算「成功写入快照」，否则保留上次数据。
	write.Success = len(snap.Instances) > 0 || len(snap.Users) > 0 || len(snap.Variables) > 0 || snap.Normalized != nil

	if len(snap.ChanErrors) > 0 {
		parts := make([]string, 0, len(snap.ChanErrors))
		for k, v := range snap.ChanErrors {
			parts = append(parts, fmt.Sprintf("%s: %s", k, v))
		}
		sort.Strings(parts)
		write.ErrorMessage = strings.Join(parts, " | ")
	}

	if snap.Normalized != nil {
		write.Version = snap.Normalized.Version
		write.Arch = snap.Normalized.Arch
		write.DashboardURLs = encodeURLList(snap.Normalized.DashboardURLs)
		write.PrometheusURLs = encodeURLList(snap.Normalized.PrometheusURLs)
		write.GrafanaURLs = encodeURLList(snap.Normalized.GrafanaURLs)
		write.AlertmanagerURLs = encodeURLList(snap.Normalized.AlertmanagerURLs)
	} else {
		write.Version = clusterVersionOf(snap)
	}
	if write.Version == "" && snap.ClusterInfo != nil {
		write.Version = snap.ClusterInfo.TiDBVersion
	}
	write.DeployMode = snap.DeployModeHint
	if write.DeployMode == "" && snap.ClusterInfo != nil {
		write.DeployMode = collector.NormalizeDeployMode(snap.ClusterInfo.DeployMode)
	}

	componentSet := map[string]bool{}
	hostSet := map[string]bool{}
	for _, inst := range snap.Instances {
		componentSet[inst.Component] = true
		hostSet[inst.HostIP] = true
	}
	write.ComponentCount = len(componentSet)
	write.HostCount = len(hostSet)
	write.InstanceCount = len(snap.Instances)

	if snap.Capabilities != nil {
		write.Capabilities = collector.MarshalCapabilities(snap.Capabilities)
	}

	// hosts
	now := write.FetchedAt
	for _, h := range snap.Hosts {
		host := model.Host{
			IP:           h.IP,
			Hostname:     h.Hostname,
			OSUser:       h.OSUser,
			CPUArch:      h.Arch,
			CPUCores:     h.CPUCores,
			MemoryGB:     h.MemoryGB,
			DiskTotalGB:  h.DiskTotalGB,
			CPUUsagePct:  h.CPUUsagePct,
			MemUsagePct:  h.MemUsagePct,
			DiskUsagePct: h.DiskUsagePct,
			SSHStatus:    model.SSHStatusUnknown,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if h.Arch != "" || h.CPUCores > 0 {
			t := now
			host.ResourceFetchedAt = &t
		}
		write.Hosts = append(write.Hosts, host)
	}
	// instances
	for _, inst := range snap.Instances {
		write.Instances = append(write.Instances, model.Instance{
			ClusterID:  clusterID,
			HostIP:     inst.HostIP,
			Component:  inst.Component,
			Role:       inst.Role,
			Port:       inst.Port,
			StatusPort: inst.StatusPort,
			Status:     inst.Status,
			Version:    inst.Version,
			StartedAt:  inst.StartedAt,
			AZ:         inst.AZ,
			DeployDir:  inst.DeployDir,
			DataDir:    inst.DataDir,
			Raw:        inst.Raw,
			FetchedAt:  now,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
	}
	// users
	for _, u := range snap.Users {
		write.Users = append(write.Users, model.TiDBUser{
			ClusterID:       clusterID,
			UserName:        u.UserName,
			Host:            u.Host,
			AuthPlugin:      u.AuthPlugin,
			AccountLocked:   u.AccountLocked,
			PasswordExpired: u.PasswordExpired,
			ResourceGroup:   u.ResourceGroup,
			FetchedAt:       now,
			CreatedAt:       now,
		})
	}
	// resource groups
	for _, rg := range snap.RGs {
		burstable := 0
		if rg.Burstable {
			burstable = 1
		}
		write.RGs = append(write.RGs, model.ResourceGroup{
			ClusterID: clusterID,
			Name:      rg.Name,
			RUPerSec:  rg.RUPerSec,
			Priority:  rg.Priority,
			Burstable: burstable,
			UserCount: rg.UserCount,
			Status:    rg.Status,
			FetchedAt: now,
			CreatedAt: now,
		})
	}
	// sessions
	for _, se := range snap.Sessions {
		write.Sessions = append(write.Sessions, model.Session{
			ClusterID: clusterID,
			SessionID: se.ID,
			Instance:  se.Instance,
			UserName:  se.UserName,
			Host:      se.Host,
			DB:        se.DB,
			Command:   se.Command,
			TimeSec:   se.TimeSec,
			State:     se.State,
			SQLText:   se.SQLText,
			SQLDigest: se.SQLDigest,
			FetchedAt: now,
			CreatedAt: now,
		})
	}
	// variables
	for _, v := range snap.Variables {
		differs := 0
		if v.DefaultValue != "" && v.Value != v.DefaultValue {
			differs = 1
		}
		dynamic := 0
		if v.IsDynamic {
			dynamic = 1
		}
		write.Variables = append(write.Variables, model.Variable{
			ClusterID:    clusterID,
			Name:         v.Name,
			Value:        v.Value,
			DefaultValue: v.DefaultValue,
			Scope:        v.Scope,
			IsDynamic:    dynamic,
			Description:  v.Description,
			Differs:      differs,
			FetchedAt:    now,
			CreatedAt:    now,
		})
	}

	return s.clusters.ApplySnapshot(ctx, write)
}

// clusterVersionOf 从快照提取版本。
func clusterVersionOf(snap *collector.Snapshot) string {
	if snap == nil {
		return ""
	}
	if snap.Normalized != nil && snap.Normalized.Version != "" {
		return snap.Normalized.Version
	}
	if snap.ClusterInfo != nil {
		return snap.ClusterInfo.TiDBVersion
	}
	if snap.Meta != nil {
		return snap.Meta.ClusterVersion
	}
	return ""
}

// ---------------------------------------------------------------------------
// 详情子接口
// ---------------------------------------------------------------------------

// InstanceSummary 实例状态摘要。
type InstanceSummary struct {
	Total     int `json:"total"`
	Up        int `json:"up"`
	Down      int `json:"down"`
	Unhealthy int `json:"unhealthy"`
	Unknown   int `json:"unknown"`
}

// Overview 集群概览。
type ClusterOverview struct {
	Cluster           *ClusterDetail      `json:"cluster"`
	Components        []repo.ComponentAgg `json:"components"`
	VersionConsistent bool                `json:"versionConsistent"`
	Versions          []string            `json:"versions"`
	InstanceSummary   InstanceSummary     `json:"instanceSummary"`
	RecentJobs        []model.Job         `json:"recentJobs"`
	FetchedAt         *time.Time          `json:"fetchedAt"`
	CollectError      string              `json:"collectError"`
	PDUnavailable     bool                `json:"pdUnavailable"`
}

// Overview 返回集群概览数据。
func (s *ClusterService) Overview(ctx context.Context, id int64) (*ClusterOverview, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	comps, err := s.instances.AggregateComponents(ctx, id)
	if err != nil {
		return nil, apperr.Internal("聚合组件信息失败")
	}
	versionSet := map[string]bool{}
	summary := InstanceSummary{}
	for _, comp := range comps {
		summary.Total += comp.InstanceCount
		summary.Up += comp.UpCount
		summary.Down += comp.DownCount
		for _, v := range comp.Versions {
			versionSet[v] = true
		}
	}
	versions := make([]string, 0, len(versionSet))
	for v := range versionSet {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	summary.Unknown = summary.Total - summary.Up - summary.Down

	jobs, _ := s.jobs.RecentByCluster(ctx, id, 5)
	if jobs == nil {
		jobs = []model.Job{}
	}
	return &ClusterOverview{
		Cluster:           detail,
		Components:        comps,
		VersionConsistent: len(versions) <= 1,
		Versions:          versions,
		InstanceSummary:   summary,
		RecentJobs:        jobs,
		FetchedAt:         detail.FetchedAt,
		CollectError:      detail.LastCollectError,
		PDUnavailable:     detail.Capabilities != nil && detail.Capabilities.ProbeErrors != nil && detail.Capabilities.ProbeErrors["pd"] != "",
	}, nil
}

// TopologyNode 拓扑节点（一个实例）。
type TopologyNode struct {
	ID         int64  `json:"id"`
	HostIP     string `json:"hostIp"`
	Component  string `json:"component"`
	Port       int    `json:"port"`
	StatusPort int    `json:"statusPort"`
	Status     string `json:"status"`
	Role       string `json:"role"`
	Version    string `json:"version"`
	AZ         string `json:"az"`
	// RawHostIP 采集到的原始地址；仅在地址映射生效且改写成功时填写，
	// 便于界面标注「这是内网地址，实际访问用 hostIp」。
	RawHostIP string `json:"rawHostIp,omitempty"`
}

// TopologyGroup 按组件分组的拓扑。
type TopologyGroup struct {
	Component string         `json:"component"`
	Nodes     []TopologyNode `json:"nodes"`
}

// TopologyHost 按主机聚合的拓扑：一台主机上承载了哪些实例。
type TopologyHost struct {
	HostIP        string  `json:"hostIp"`
	RawHostIP     string  `json:"rawHostIp,omitempty"`
	Hostname      string  `json:"hostname"`
	SSHStatus     string  `json:"sshStatus"`
	CPUCores      int     `json:"cpuCores"`
	MemoryGB      float64 `json:"memoryGb"`
	InstanceCount int     `json:"instanceCount"`
	// Components 该主机上出现的组件（去重、按组件序），便于前端直接渲染标签。
	Components []string       `json:"components"`
	Instances  []TopologyNode `json:"instances"`
}

// TopologyResult 拓扑聚合结果：同一份数据给出「按主机」与「按组件」两个视角。
type TopologyResult struct {
	Hosts     []TopologyHost  `json:"hosts"`
	Groups    []TopologyGroup `json:"groups"`
	NodeCount int             `json:"nodeCount"`
	HostCount int             `json:"hostCount"`
}

// Topology 返回拓扑图数据（节点数恒等于实例数）。
//
// 以「主机 → 实例」为主视角（用户真正关心的是某台机器上跑着什么），
// 同时保留按组件分组的视角，前端可切换。
func (s *ClusterService) Topology(ctx context.Context, id int64) (*TopologyResult, error) {
	if _, err := s.clusters.GetByID(ctx, id); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	list, err := s.instances.ListAll(ctx, id)
	if err != nil {
		return nil, apperr.Internal("查询实例失败")
	}

	// 地址映射：拓扑展示的是「TMP 视角可达的地址」。原始地址保留在 RawHostIP 里，
	// 主机元信息也要按映射后的 IP 关联，否则映射生效后会丢主机名 / CPU / 内存。
	mp := s.addrMapper(ctx, id)
	displayIP := func(raw string) (mapped, original string) {
		v := strings.TrimSpace(raw)
		if v == "" {
			return "unknown", ""
		}
		if m, ok := mp.MapIP(v); ok {
			return m, v
		}
		return v, ""
	}

	byComp := map[string][]TopologyNode{}
	compOrder := []string{}
	byHost := map[string][]TopologyNode{}
	hostOrder := []string{}
	hostRawByIP := map[string]string{}
	for _, inst := range list {
		ip, raw := displayIP(inst.HostIP)
		node := TopologyNode{
			ID:         inst.ID,
			HostIP:     ip,
			RawHostIP:  raw,
			Component:  inst.Component,
			Port:       inst.Port,
			StatusPort: inst.StatusPort,
			Status:     inst.Status,
			Role:       inst.Role,
			Version:    inst.Version,
			AZ:         inst.AZ,
		}
		if _, ok := byComp[inst.Component]; !ok {
			compOrder = append(compOrder, inst.Component)
		}
		byComp[inst.Component] = append(byComp[inst.Component], node)

		if _, ok := byHost[ip]; !ok {
			hostOrder = append(hostOrder, ip)
			if raw != "" {
				hostRawByIP[ip] = raw
			}
		}
		byHost[ip] = append(byHost[ip], node)
	}
	sort.SliceStable(compOrder, func(i, j int) bool {
		return componentSortKey(compOrder[i]) < componentSortKey(compOrder[j])
	})
	// 同一主机内的实例按「组件业务序 + 端口」排列，读起来才像一份部署清单。
	for ip := range byHost {
		nodes := byHost[ip]
		sort.SliceStable(nodes, func(i, j int) bool {
			ki, kj := componentSortKey(nodes[i].Component), componentSortKey(nodes[j].Component)
			if ki != kj {
				return ki < kj
			}
			return nodes[i].Port < nodes[j].Port
		})
		byHost[ip] = nodes
	}

	// 主机元信息（主机名 / SSH / CPU / 内存）按 IP 关联，取不到就用占位值。
	// 映射生效时按「映射后的 IP」建索引，与上面 byHost 的键保持一致。
	metaByIP := map[string]model.Host{}
	if hosts, herr := s.hosts.ListByCluster(ctx, id); herr == nil {
		for _, h := range hosts {
			key := h.IP
			if m, ok := mp.MapIP(key); ok {
				key = m
			}
			metaByIP[key] = h
		}
	}

	sort.SliceStable(hostOrder, func(i, j int) bool { return ipLess(hostOrder[i], hostOrder[j]) })
	res := &TopologyResult{
		Hosts:     make([]TopologyHost, 0, len(hostOrder)),
		Groups:    make([]TopologyGroup, 0, len(compOrder)),
		NodeCount: len(list),
		HostCount: len(hostOrder),
	}
	for _, ip := range hostOrder {
		nodes := byHost[ip]
		h := TopologyHost{HostIP: ip, RawHostIP: hostRawByIP[ip], InstanceCount: len(nodes), Instances: nodes}
		if m, ok := metaByIP[ip]; ok {
			h.Hostname = m.Hostname
			h.SSHStatus = m.SSHStatus
			h.CPUCores = m.CPUCores
			h.MemoryGB = m.MemoryGB
		}
		seen := map[string]bool{}
		comps := make([]string, 0, 4)
		for _, n := range nodes {
			if !seen[n.Component] {
				seen[n.Component] = true
				comps = append(comps, n.Component)
			}
		}
		sort.SliceStable(comps, func(i, j int) bool { return componentSortKey(comps[i]) < componentSortKey(comps[j]) })
		h.Components = comps
		res.Hosts = append(res.Hosts, h)
	}
	for _, comp := range compOrder {
		res.Groups = append(res.Groups, TopologyGroup{Component: comp, Nodes: byComp[comp]})
	}
	return res, nil
}

// ipLess 按数值段比较 IP，避免 10.0.0.10 排在 10.0.0.9 前面。
func ipLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea != nil || eb != nil {
			break
		}
		if na != nb {
			return na < nb
		}
	}
	return a < b
}

// componentSortKey 组件排序键。
func componentSortKey(c string) int {
	order := map[string]int{
		"pd": 0, "tidb": 1, "tikv": 2, "tiflash": 3, "ticdc": 4,
		"prometheus": 5, "grafana": 6, "alertmanager": 7,
	}
	if v, ok := order[c]; ok {
		return v
	}
	return 99
}

// Instances 分页查询实例。
func (s *ClusterService) Instances(ctx context.Context, id int64, f repo.InstanceFilter) (*repo.ListResult[model.Instance], error) {
	if _, err := s.clusters.GetByID(ctx, id); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	list, total, err := s.instances.List(ctx, id, f)
	if err != nil {
		return nil, apperr.Internal("查询实例失败")
	}
	// 地址映射：展示可达地址，原始内网地址留在 RawHostIP。
	mp := s.addrMapper(ctx, id)
	for i := range list {
		if mapped, ok := mp.MapIP(list[i].HostIP); ok {
			list[i].RawHostIP = list[i].HostIP
			list[i].HostIP = mapped
		}
	}
	res := repo.NewListResult(list, total, f.Page)
	return &res, nil
}

// ComponentView 组件聚合视图。
type ComponentView struct {
	Component     string   `json:"component"`
	InstanceCount int      `json:"instanceCount"`
	Versions      []string `json:"versions"`
	UpCount       int      `json:"upCount"`
	DownCount     int      `json:"downCount"`
}

// ComponentsInfo 组件与版本信息。
type ComponentsInfo struct {
	Components        []repo.ComponentAgg `json:"components"`
	VersionConsistent bool                `json:"versionConsistent"`
	Versions          []string            `json:"versions"`
}

// Components 按组件聚合实例数与版本。
func (s *ClusterService) Components(ctx context.Context, id int64) (*ComponentsInfo, error) {
	if _, err := s.clusters.GetByID(ctx, id); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	comps, err := s.instances.AggregateComponents(ctx, id)
	if err != nil {
		return nil, apperr.Internal("聚合组件信息失败")
	}
	versionSet := map[string]bool{}
	for _, comp := range comps {
		for _, v := range comp.Versions {
			versionSet[v] = true
		}
	}
	versions := make([]string, 0, len(versionSet))
	for v := range versionSet {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return &ComponentsInfo{Components: comps, VersionConsistent: len(versions) <= 1, Versions: versions}, nil
}

// CapabilitiesInfo 能力信息。
type CapabilitiesInfo struct {
	Capabilities   *collector.Capabilities `json:"capabilities"`
	FeatureVisible map[string]bool         `json:"featureVisible"`
	TiDBVersion    string                  `json:"tidbVersion"`
}

// Capabilities 返回版本能力与 UI 显隐映射。
func (s *ClusterService) Capabilities(ctx context.Context, id int64) (*CapabilitiesInfo, error) {
	c, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	var caps *collector.Capabilities
	if c.Capabilities != "" {
		var parsed collector.Capabilities
		if err := json.Unmarshal([]byte(c.Capabilities), &parsed); err == nil {
			// 支持矩阵可能已更新（如新增 v7.1.8-5.x 回移分支），按当前规则校正快照。
			caps = collector.NormalizeCapabilities(&parsed, c.TiDBVersion)
		}
	}
	if caps == nil {
		caps = collector.BuildCapabilities(c.TiDBVersion, nil)
	}
	return &CapabilitiesInfo{
		Capabilities:   caps,
		FeatureVisible: collector.FeatureVisibility(caps),
		TiDBVersion:    c.TiDBVersion,
	}, nil
}

// ChangeItem 变更记录条目。
type ChangeItem struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Username  string    `json:"username"`
	Result    string    `json:"result"`
	Detail    string    `json:"detail"`
	JobID     *int64    `json:"jobId"`
	CreatedAt time.Time `json:"createdAt"`
}

// Changes 变更记录（audit_logs + jobs 聚合）。
func (s *ClusterService) Changes(ctx context.Context, id int64, limit int) ([]ChangeItem, error) {
	if _, err := s.clusters.GetByID(ctx, id); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	logs, _, err := s.audits.List(ctx, repo.AuditFilter{
		ResourceType: "cluster",
		ResourceID:   fmt.Sprintf("%d", id),
		Page:         repo.Page{Page: 1, PageSize: limit},
	})
	if err != nil {
		return nil, apperr.Internal("查询变更记录失败")
	}
	out := make([]ChangeItem, 0, len(logs))
	for _, l := range logs {
		out = append(out, ChangeItem{
			ID:        fmt.Sprintf("audit-%d", l.ID),
			Action:    l.Action,
			Username:  l.Username,
			Result:    l.Result,
			Detail:    l.Detail,
			CreatedAt: l.CreatedAt,
		})
	}
	jobs, _ := s.jobs.RecentByCluster(ctx, id, 20)
	for i := range jobs {
		j := jobs[i]
		jobID := j.ID
		out = append(out, ChangeItem{
			ID:        fmt.Sprintf("job-%d", j.ID),
			Action:    "job." + j.Type,
			Username:  j.CreatedByName,
			Result:    j.Status,
			Detail:    j.ErrorSummary,
			JobID:     &jobID,
			CreatedAt: j.CreatedAt,
		})
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].CreatedAt.After(out[k].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Metrics 指标趋势（MVP 未接入监控，返回 40901）。
func (s *ClusterService) Metrics(ctx context.Context, id int64, rng string) error {
	if _, err := s.clusters.GetByID(ctx, id); err != nil {
		return apperr.NotFound("集群不存在")
	}
	return apperr.Conflict("未接入监控系统（Prometheus），性能趋势不可用")
}

// Alerts 活跃告警（MVP 返回空数组，P2 接入 Prometheus）。
func (s *ClusterService) Alerts(ctx context.Context, id int64) ([]map[string]any, error) {
	if _, err := s.clusters.GetByID(ctx, id); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	return []map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// 演示集群初始化
// ---------------------------------------------------------------------------

// SeedMockClusters 演示模式下首次启动自动创建 3 套假集群并触发采集。
//
// 返回新建的集群 ID 列表；已存在集群时不重复创建。
func (s *ClusterService) SeedMockClusters(ctx context.Context) ([]int64, error) {
	if !s.mode.IsMock() {
		return nil, nil
	}
	existing, err := s.clusters.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, nil
	}
	type mockSpec struct {
		name    string
		hosts   []string
		version string
	}
	specs := []mockSpec{
		{name: "mock-prod-a", hosts: []string{"10.20.1.11", "10.20.1.12", "10.20.1.13", "10.20.1.14"}, version: "v7.5.0"},
		{name: "mock-prod-b", hosts: []string{"10.30.2.21", "10.30.2.22", "10.30.2.23"}, version: "v6.5.4"},
		{name: "mock-test-c", hosts: []string{"10.40.3.31", "10.40.3.32", "10.40.3.33"}, version: "v8.5.0"},
	}
	now := time.Now()
	ids := make([]int64, 0, len(specs))
	for _, sp := range specs {
		c := &model.Cluster{
			Name:      sp.name,
			NameLower: strings.ToLower(sp.name),
			Draft:     0,
			// 内置演示集群同样打上演示标记，列表与详情会显示「演示数据」。
			IsDemo:        1,
			TiDBVersion:   sp.version,
			DeployMode:    model.DeployModeUnknown,
			CPUArch:       "x86_64",
			TiUPHost:      sp.hosts[0],
			TiUPPort:      22,
			TiUPUser:      "tidb",
			TiUPDir:       "/home/tidb/.tiup",
			DBHost:        sp.hosts[0],
			DBPort:        4000,
			DBUser:        "root",
			PDEndpoint:    "http://" + sp.hosts[0] + ":2379",
			Status:        model.ClusterStatusUnknown,
			CollectStatus: model.ClusterStatusUnknown,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := s.clusters.Create(ctx, c); err != nil {
			return ids, err
		}
		ids = append(ids, c.ID)
		// 演示模式凭据无意义，但仍写入占位密文以保持凭据列有数据。
		_ = s.saveCredentials(ctx, c.ID, "demo-password", "", "demo-password", nil)
	}

	// 触发首轮采集，任务中心因此可看到完整任务与日志。
	for _, id := range ids {
		clusterID := id
		name := ""
		if c, err := s.clusters.GetByID(ctx, clusterID); err == nil {
			name = c.Name
		}
		_ = s.clusters.SetCollecting(ctx, clusterID, 1)
		if _, err := s.engine.Submit(ctx, &model.Job{
			Type:          job.TypeCollect,
			ClusterID:     &clusterID,
			TargetType:    "cluster",
			TargetLabel:   name,
			CreatedByName: "system",
			Payload:       job.Marshal(job.CollectPayload{ClusterID: clusterID}),
		}); err != nil {
			_ = s.clusters.SetCollecting(ctx, clusterID, 0)
		}
	}
	return ids, nil
}
