package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// CompositeCollector 真实模式采集器：聚合 SSH / MySQL / PD 三通道，并行采集互不阻塞。
type CompositeCollector struct {
	ssh      SSHChannel
	mysql    MySQLChannel
	pd       PDChannel
	timeouts *CommandTimeouts
}

// NewCompositeCollector 构造真实模式采集器。
//
// SSH 的单命令超时由 timeouts 提供（系统设置可调，默认 30s）；
// MySQL / PD 仍用固定的 ChannelTimeout。
func NewCompositeCollector(timeouts *CommandTimeouts) *CompositeCollector {
	if timeouts == nil {
		timeouts = NewCommandTimeouts(0)
	}
	return &CompositeCollector{
		ssh:      NewSSHChannel(timeouts),
		mysql:    NewMySQLChannel(ChannelTimeout),
		pd:       NewPDChannel(ChannelTimeout),
		timeouts: timeouts,
	}
}

// Kind 真实模式标识为 ssh 通道类型。
func (c *CompositeCollector) Kind() Kind { return KindSSH }

// Probe 三通道并行探测，每项独立 5s 超时，整体不超过 15s（PRD R-06）。
//
// 关键：任一通道失败或超时都必须返回结果（ok=false + message），绝不整体 hang 死。
func (c *CompositeCollector) Probe(ctx context.Context, t *Target, scopes []ProbeScope) ([]ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, TotalTimeout)
	defer cancel()

	type item struct {
		scope ProbeScope
		fn    func(context.Context) (ProbeResult, error)
	}
	items := []item{
		{ScopeSSH, func(cx context.Context) (ProbeResult, error) { return c.ssh.Probe(cx, t) }},
		{ScopeMySQL, func(cx context.Context) (ProbeResult, error) { return c.mysql.Probe(cx, t) }},
		{ScopePD, func(cx context.Context) (ProbeResult, error) { return c.pd.Probe(cx, t) }},
	}

	results := make([]ProbeResult, len(items))
	var wg sync.WaitGroup
	for i, it := range items {
		if !scopeWanted(scopes, it.scope) {
			results[i] = ProbeResult{Scope: it.scope, OK: false, Message: "未执行该项探测"}
			continue
		}
		wg.Add(1)
		go func(idx int, fn func(context.Context) (ProbeResult, error), scope ProbeScope) {
			defer wg.Done()
			itemCtx, itemCancel := context.WithTimeout(ctx, ProbeItemTimeout)
			defer itemCancel()

			done := make(chan ProbeResult, 1)
			go func() {
				res, err := fn(itemCtx)
				if err != nil {
					res = ProbeResult{Scope: scope, OK: false, Message: sanitizeCredential(err.Error())}
				}
				if res.Scope == "" {
					res.Scope = scope
				}
				done <- res
			}()
			select {
			case res := <-done:
				results[idx] = res
			case <-itemCtx.Done():
				results[idx] = ProbeResult{Scope: scope, OK: false, LatencyMS: ProbeItemTimeout.Milliseconds(), Message: "探测超时（5s）"}
			}
		}(i, it.fn, it.scope)
	}
	wg.Wait()

	// 只返回本次请求的通道结果，且保持 SSH → MySQL → PD 的固定顺序。
	//
	// 注意：不要返回「未执行该项探测」的占位项——调用方（如接管任务的步骤）会按下标
	// 取第一个结果做判定，占位项会导致「明明要测 MySQL 却拿到 SSH 占位」而误判失败。
	out := make([]ProbeResult, 0, len(items))
	for i, it := range items {
		if !scopeWanted(scopes, it.scope) {
			continue
		}
		out = append(out, results[i])
	}
	return out, nil
}

// Capabilities 通过 MySQL 通道探测版本能力。
func (c *CompositeCollector) Capabilities(ctx context.Context, t *Target) (*Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, ChannelTimeout)
	defer cancel()
	return c.mysql.Capabilities(ctx, t)
}

// Exec 执行白名单写语句。
func (c *CompositeCollector) Exec(ctx context.Context, t *Target, st Statement) error {
	ctx, cancel := context.WithTimeout(ctx, ChannelTimeout)
	defer cancel()
	return c.mysql.Exec(ctx, t, st)
}

// Collect 三通道并行采集并归一化为 Snapshot。
//
// 通道错误全部记入 Snapshot.ChanErrors，由 Derive 统一推导健康状态；
// 采集过程绝不因单通道失败而整体返回 error（除非上下文被取消）。
func (c *CompositeCollector) Collect(ctx context.Context, t *Target, sc CollectScope) (*Snapshot, error) {
	// 整体窗口取「SSH 通道窗口」与 TotalTimeout 的较大者：SSH 单命令上限
	// 可配（默认 30s，比固定的 15s 长），不放大窗口会把大集群的正常
	// display 卡在 TotalTimeout 上。
	window := TotalTimeout
	if w := c.timeouts.SSHWindow(); w > window {
		window = w
	}
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	snap := &Snapshot{
		ChanErrors: map[Kind]string{},
		FetchedAt:  time.Now(),
	}
	var mu sync.Mutex
	setErr := func(k Kind, err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		snap.ChanErrors[k] = sanitizeCredential(err.Error())
	}

	var (
		meta      *TiUPMeta
		hostRes   []HostResource
		hwRes     []HostResource
		clusterIn *ClusterInfo
		users     []TiDBUserRow
		vars      []VariableRow
		sessions  []SessionRow
		rgs       []RGRow
		stores    []Store
		pdStatus  *PDClusterStatus
		caps      *Capabilities
	)

	var g errgroup.Group

	// 通道 1：SSH —— 拓扑与主机资源。
	if sc.Meta {
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, c.timeouts.SSHWindow())
			defer ccancel()
			m, err := c.ssh.FetchTiUPMeta(cctx, t)
			if err != nil {
				setErr(KindSSH, err)
			} else {
				meta = m
			}
			return nil
		})
	}
	if sc.Hosts {
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, c.timeouts.SSHWindow())
			defer ccancel()
			h, err := c.ssh.FetchHostResource(cctx, t)
			if err == nil {
				hostRes = h
			}
			return nil
		})
		// 其余主机的硬件信息走 MySQL（cluster_hardware / cluster_load），
		// 与 SSH 通道并行执行，避免逐台 SSH 登录。两者互不阻塞。
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
			defer ccancel()
			h, err := c.mysql.FetchHostHardware(cctx, t)
			if err != nil {
				// 注意：这里**绝不能** setErr(KindMySQL)——硬件表只是「可选增强」，
				// 因权限/版本不可用属于能力问题，若记成 MySQL 通道故障，
				// Derive 会把本来健康的集群误判为 degraded/error。
				// 降级行为：其余主机的硬件留空，由 synthesizeHosts 补仅 IP 占位。
				return nil
			}
			hwRes = h
			return nil
		})
	}

	// 通道 2：MySQL —— 集群信息、用户、参数、会话、RG。
	g.Go(func() error {
		cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
		defer ccancel()
		info, err := c.mysql.FetchClusterInfo(cctx, t)
		if err != nil {
			setErr(KindMySQL, err)
			return nil
		}
		clusterIn = info
		return nil
	})
	if sc.Users {
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
			defer ccancel()
			if v, err := c.mysql.FetchUsers(cctx, t); err == nil {
				users = v
			}
			return nil
		})
	}
	if sc.Variables {
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
			defer ccancel()
			if v, err := c.mysql.FetchVariables(cctx, t); err == nil {
				vars = v
			}
			return nil
		})
	}
	if sc.Sessions {
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
			defer ccancel()
			if v, err := c.mysql.FetchSessions(cctx, t); err == nil {
				sessions = v
			}
			return nil
		})
	}
	if sc.RGs {
		g.Go(func() error {
			cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
			defer ccancel()
			// 低版本返回 ErrUnsupported，这是能力问题而非通道故障，不记入 ChanErrors。
			if v, err := c.mysql.FetchResourceGroups(cctx, t); err == nil {
				rgs = v
			}
			return nil
		})
	}
	g.Go(func() error {
		cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
		defer ccancel()
		if v, err := c.mysql.Capabilities(cctx, t); err == nil {
			caps = v
		}
		return nil
	})

	// 通道 3：PD —— stores / 集群状态。
	g.Go(func() error {
		cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
		defer ccancel()
		status, err := c.pd.FetchClusterStatus(cctx, t)
		if err != nil {
			setErr(KindPD, err)
			return nil
		}
		pdStatus = status
		if v, sErr := c.pd.FetchStores(cctx, t); sErr == nil {
			stores = v
		}
		return nil
	})

	_ = g.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 归一化：display → 实例 / 地址 / 版本 / 架构。
	// display 不可用时 meta 为 nil，实例为空 —— 上层据此显示「实例信息获取失败」，
	// 不再用 meta.yaml 或 PD stores 拼一份降级拓扑。
	normalized := NormalizeMeta(meta, t.ClusterName)
	instances := MergeInstances(normalized.Instances, stores)

	snap.Meta = meta
	snap.Instances = instances
	// 主机资源逐层补齐：SSH（中控机实测，最权威）→ MySQL cluster_hardware/load（其余主机）
	// → 仅 IP 占位（保证实例数 ≥ 主机数自洽）。每一层只补空，不覆盖已有值。
	mergedHosts := FillHostResources(hostRes, hwRes)
	mergedHosts = MergeHostResources(mergedHosts, synthesizeHosts(instances, mergedHosts))
	snap.Hosts = mergedHosts
	snap.Users = users
	snap.Variables = vars
	snap.Sessions = sessions
	snap.RGs = rgs
	snap.ClusterInfo = clusterIn
	snap.Capabilities = caps
	snap.Stores = stores

	// 版本兜底：meta.cluster_version → MySQL SELECT VERSION()。
	if normalized.Version != "" {
		snap.ClusterInfo = ensureClusterInfo(snap.ClusterInfo)
		snap.ClusterInfo.TiDBVersion = normalized.Version
	} else if clusterIn != nil && clusterIn.TiDBVersion != "" {
		normalized.Version = clusterIn.TiDBVersion
	}
	// 部署模式兜底：PD replication mode → unknown。
	if pdStatus != nil && pdStatus.ReplicationMode != "" {
		meta = ensureMeta(meta)
		meta.ClusterVersion = normalized.Version
		snap.DeployModeHint = NormalizeDeployMode(pdStatus.ReplicationMode)
	}
	// 架构兜底：SSH uname -m / MySQL cpu-arch 均可提供，取首个非空值。
	if normalized.Arch == "" {
		for _, h := range snap.Hosts {
			if h.Arch != "" {
				normalized.Arch = h.Arch
				break
			}
		}
	}
	snap.Normalized = normalized
	if meta != nil {
		snap.Source = meta.Source
	}
	snap.Consistency = crossCheckStatus(snap)
	return snap, nil
}

// crossCheckStatus 用平台自有通道的实测结果，交叉校验 TiUP 报告的实例状态。
//
// `tiup cluster display` 的状态列是 TiUP 自己发起的「一次性探测」结果，
// 会因瞬时超时把整排实例误报为 Down（曾在集群全程无重启时出现全 Down、
// Dashboard URL 整行缺失的形态）。这类误判只能靠旁证提示，不能靠改写状态解决。
//
// 注意：本函数只产出「告警文案」，绝不参与实例状态赋值 —— 状态唯一来源仍是
// `tiup cluster display`。发现矛盾时把话说明白，交给用户判断。
func crossCheckStatus(s *Snapshot) []string {
	if s == nil || s.Normalized == nil || len(s.Instances) == 0 {
		return nil
	}
	var pdDown, tidbDown, tikvDown []string
	tidbTotal, tikvTotal := 0, 0
	for _, i := range s.Instances {
		switch i.Component {
		case "tidb":
			tidbTotal++
		case "tikv":
			tikvTotal++
		}
		if i.Status == "up" {
			continue
		}
		switch i.Component {
		case "pd":
			pdDown = append(pdDown, i.HostIP)
		case "tidb":
			tidbDown = append(tidbDown, i.HostIP)
		case "tikv":
			tikvDown = append(tikvDown, i.HostIP)
		}
	}
	pdOK := s.ChanErrors[KindPD] == ""
	mysqlOK := s.ChanErrors[KindMySQL] == ""

	var out []string
	// 1) PD 的 2379 实测可访问，但 TiUP 说 PD 不在运行。
	if pdOK && len(pdDown) > 0 {
		out = append(out, fmt.Sprintf(
			"PD 服务接口实测可访问，但 TiUP 报告 pd(%s) 状态非 up —— 该状态很可能是一次失败的探测",
			strings.Join(pdDown, ", ")))
	}
	// 2) MySQL 协议能连上，但 TiUP 说所有 TiDB 都不在运行。
	if mysqlOK && tidbTotal > 0 && len(tidbDown) == tidbTotal {
		out = append(out, fmt.Sprintf(
			"MySQL 4000 端口实测可连接，但 TiUP 报告全部 %d 个 tidb 实例状态非 up —— 两者矛盾，请以 MySQL 连通性为准复核",
			tidbTotal))
	}
	// 3) PD 里能取到 up 的 store，但 TiUP 说所有 TiKV 都不在运行。
	if pdOK && tikvTotal > 0 && len(tikvDown) == tikvTotal && hasUpStore(s.Stores) {
		out = append(out, fmt.Sprintf(
			"PD 中存在 up 状态的 TiKV store，但 TiUP 报告全部 %d 个 tikv 实例状态非 up —— 两者矛盾",
			tikvTotal))
	}
	// 4) 反向：拓扑一片正常，但 MySQL 根本连不上 —— display 与实际连通性矛盾。
	if !mysqlOK && tidbTotal > 0 && len(tidbDown) == 0 {
		out = append(out, fmt.Sprintf(
			"TiUP 显示 %d 个 tidb 实例均正常，但 MySQL 通道连不上（%s）—— 请复核实例状态与实际连通性",
			tidbTotal, s.ChanErrors[KindMySQL]))
	}
	return out
}

// hasUpStore 判断 PD stores 中是否存在处于 up 状态的 TiKV。
func hasUpStore(stores []Store) bool {
	for _, st := range stores {
		if strings.EqualFold(strings.TrimSpace(st.StateName), "Up") {
			return true
		}
	}
	return false
}

// synthesizeHosts 为仅有 IP 的实例补一条主机记录，保证主机数与拓扑一致。
func synthesizeHosts(instances []InstanceSnapshot, existing []HostResource) []HostResource {
	have := map[string]bool{}
	for _, h := range existing {
		have[h.IP] = true
	}
	out := make([]HostResource, 0)
	for _, ip := range HostIPsFromInstances(instances) {
		if have[ip] {
			continue
		}
		out = append(out, HostResource{IP: ip})
	}
	return out
}

// ensureMeta 保证 meta 非 nil。
func ensureMeta(m *TiUPMeta) *TiUPMeta {
	if m == nil {
		return &TiUPMeta{}
	}
	return m
}

// ensureClusterInfo 保证 ClusterInfo 非 nil。
func ensureClusterInfo(c *ClusterInfo) *ClusterInfo {
	if c == nil {
		return &ClusterInfo{}
	}
	return c
}

// MarshalCapabilities 序列化能力快照到 clusters.capabilities（JSON 字符串）。
func MarshalCapabilities(caps *Capabilities) string {
	if caps == nil {
		return ""
	}
	b, err := json.Marshal(caps)
	if err != nil {
		return ""
	}
	return string(b)
}
