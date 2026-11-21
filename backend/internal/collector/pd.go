package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// pdChannel 通过 PD HTTP API 采集。
type pdChannel struct {
	timeout time.Duration
	client  *http.Client
}

// NewPDChannel 构造 PD 通道。
func NewPDChannel(timeout time.Duration) PDChannel {
	if timeout <= 0 {
		timeout = ChannelTimeout
	}
	return &pdChannel{
		timeout: timeout,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext:  (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
				MaxIdleConns: 4,
			},
		},
	}
}

// Kind 实现 Channel。
func (p *pdChannel) Kind() Kind { return KindPD }

// baseURL 返回 PD 基础地址。
func (p *pdChannel) baseURL(t *Target) string {
	host := t.PDHost
	if host == "" {
		return ""
	}
	port := t.PDPort
	if port <= 0 {
		port = 2379
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// get 发起 GET 并把 JSON 解码到 out。
func (p *pdChannel) get(ctx context.Context, t *Target, path string, out any) error {
	base := p.baseURL(t)
	if base == "" {
		return errors.New("未配置 PD 地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("PD 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("PD 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析 PD 响应失败: %w", err)
	}
	return nil
}

// Probe 探测 PD API 可达性。
func (p *pdChannel) Probe(ctx context.Context, t *Target) (ProbeResult, error) {
	start := time.Now()
	res := ProbeResult{Scope: ScopePD}
	var version struct {
		Version string `json:"version"`
	}
	err := p.get(ctx, t, "/pd/api/v1/version", &version)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = err.Error()
		return res, nil
	}
	res.OK = true
	res.Message = "PD API 可达"
	res.Detail = map[string]any{"version": version.Version}
	return res, nil
}

// pdStoresResp /stores 响应。
type pdStoresResp struct {
	Count  int `json:"count"`
	Stores []struct {
		Store struct {
			ID        uint64 `json:"id"`
			Address   string `json:"address"`
			StateName string `json:"state_name"`
			Version   string `json:"version"`
			Labels    []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"labels"`
			Status struct {
				LeaderCount int   `json:"leader_count"`
				RegionCount int   `json:"region_count"`
				Capacity    int64 `json:"capacity"`
				Available   int64 `json:"available"`
			} `json:"status"`
		} `json:"store"`
	} `json:"stores"`
}

// FetchStores 读取 TiKV store 列表。
func (p *pdChannel) FetchStores(ctx context.Context, t *Target) ([]Store, error) {
	var resp pdStoresResp
	if err := p.get(ctx, t, "/pd/api/v1/stores", &resp); err != nil {
		return nil, err
	}
	out := make([]Store, 0, len(resp.Stores))
	for _, s := range resp.Stores {
		labels := map[string]string{}
		for _, l := range s.Store.Labels {
			labels[l.Key] = l.Value
		}
		out = append(out, Store{
			Address:     s.Store.Address,
			StateName:   s.Store.StateName,
			Version:     s.Store.Version,
			Labels:      labels,
			LeaderCount: s.Store.Status.LeaderCount,
			RegionCount: s.Store.Status.RegionCount,
			Capacity:    s.Store.Status.Capacity,
			Available:   s.Store.Status.Available,
		})
	}
	return out, nil
}

// FetchClusterStatus 读取集群状态与副本模式。
func (p *pdChannel) FetchClusterStatus(ctx context.Context, t *Target) (*PDClusterStatus, error) {
	status := &PDClusterStatus{}
	var cluster struct {
		ClusterID int64 `json:"cluster_id"`
		ID        int64 `json:"id"`
	}
	if err := p.get(ctx, t, "/pd/api/v1/cluster/status", &cluster); err == nil {
		if cluster.ClusterID > 0 {
			status.ClusterID = cluster.ClusterID
		} else {
			status.ClusterID = cluster.ID
		}
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := p.get(ctx, t, "/pd/api/v1/version", &version); err == nil {
		status.PDVersion = version.Version
	}
	var stores struct {
		Count int `json:"count"`
	}
	if err := p.get(ctx, t, "/pd/api/v1/stores", &stores); err == nil {
		status.StoreCount = stores.Count
	}
	var regions struct {
		Count       int `json:"count"`
		RegionCount int `json:"region_count"`
		LeaderCount int `json:"leader_count"`
	}
	if err := p.get(ctx, t, "/pd/api/v1/region/count", &regions); err == nil {
		if regions.RegionCount > 0 {
			status.RegionCount = regions.RegionCount
		} else {
			status.RegionCount = regions.Count
		}
		status.LeaderCount = regions.LeaderCount
	}
	if mode, err := p.FetchReplicationMode(ctx, t); err == nil {
		status.ReplicationMode = mode
	}
	if status.ClusterID == 0 && status.PDVersion == "" && status.StoreCount == 0 {
		return nil, errors.New("PD 无有效响应")
	}
	return status, nil
}

// FetchMembers 读取 PD 成员。
func (p *pdChannel) FetchMembers(ctx context.Context, t *Target) ([]PDMember, error) {
	var resp struct {
		Members []struct {
			Name       string   `json:"name"`
			MemberID   uint64   `json:"member_id"`
			ClientURLs []string `json:"client_urls"`
		} `json:"members"`
	}
	if err := p.get(ctx, t, "/pd/api/v1/members", &resp); err != nil {
		return nil, err
	}
	out := make([]PDMember, 0, len(resp.Members))
	for _, m := range resp.Members {
		out = append(out, PDMember{Name: m.Name, MemberID: m.MemberID, ClientURLs: m.ClientURLs})
	}
	return out, nil
}

// FetchReplicationMode 读取副本模式并归一为 majority / dr_auto_sync。
func (p *pdChannel) FetchReplicationMode(ctx context.Context, t *Target) (string, error) {
	var resp struct {
		Mode   string `json:"mode"`
		State  string `json:"state"`
		DRSync struct {
			Mode  string `json:"mode"`
			State string `json:"state"`
		} `json:"dr-auto-sync"`
	}
	if err := p.get(ctx, t, "/pd/api/v1/replication_mode/status", &resp); err != nil {
		return "", err
	}
	if resp.Mode == "" && resp.DRSync.Mode == "" {
		return "unknown", nil
	}
	if resp.DRSync.Mode != "" && resp.DRSync.Mode != "async" {
		return "dr_auto_sync", nil
	}
	return NormalizeDeployMode(resp.Mode), nil
}
