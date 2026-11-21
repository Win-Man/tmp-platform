package collector

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// CompositeCollector 真实模式采集器：聚合 SSH / MySQL / PD 三通道，并行采集互不阻塞。
type CompositeCollector struct {
	ssh   SSHChannel
	mysql MySQLChannel
	pd    PDChannel
}

// NewCompositeCollector 构造真实模式采集器。
func NewCompositeCollector(timeout time.Duration) *CompositeCollector {
	return &CompositeCollector{
		ssh:   NewSSHChannel(timeout),
		mysql: NewMySQLChannel(timeout),
		pd:    NewPDChannel(timeout),
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
	return results, nil
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
	ctx, cancel := context.WithTimeout(ctx, TotalTimeout)
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
			cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
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
			cctx, ccancel := context.WithTimeout(ctx, ChannelTimeout)
			defer ccancel()
			h, err := c.ssh.FetchHostResource(cctx, t)
			if err == nil {
				hostRes = h
			}
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

	// 归一化：meta.yaml → 实例 / 地址 / 版本 / 架构。
	normalized := NormalizeMeta(meta, t.ClusterName)
	instances := MergeInstances(normalized.Instances, stores)
	if len(instances) == 0 {
		// meta 不可用时用 PD stores 反推 TiKV 实例，保证拓扑不完全空白。
		instances = instancesFromStores(stores)
	}

	snap.Meta = meta
	snap.Instances = instances
	snap.Hosts = MergeHostResources(hostRes, synthesizeHosts(instances, hostRes))
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
	// 架构兜底：SSH uname -m。
	if normalized.Arch == "" && len(hostRes) > 0 {
		normalized.Arch = hostRes[0].Arch
	}
	snap.Normalized = normalized
	return snap, nil
}

// instancesFromStores 用 PD stores 反推 TiKV 实例。
func instancesFromStores(stores []Store) []InstanceSnapshot {
	out := make([]InstanceSnapshot, 0, len(stores))
	for _, s := range stores {
		host, portStr := splitHostPort(s.Address)
		port := 0
		if portStr != "" {
			port = atoi(portStr)
		}
		out = append(out, InstanceSnapshot{
			Component:  "tikv",
			HostIP:     host,
			Port:       port,
			StatusPort: port + 80,
			Role:       "service",
			Status:     normalizeInstanceStatus(s.StateName),
			Version:    s.Version,
			AZ:         s.Labels["zone"],
		})
	}
	return out
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

// atoi 安全字符串转整型。
func atoi(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
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
