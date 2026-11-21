package collector

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// discoveryTimeout PD 自动发现整体超时（含一次 SSH 或 MySQL 建连）。
const discoveryTimeout = 10 * time.Second

// PDDiscovery PD 地址自动发现结果。
//
// 接管集群时 PD 地址无需人工填写：TiUP 部署的集群其 PD 拓扑一定写在 meta.yaml 中，
// 即使 meta.yaml 读不到，TiDB 的 information_schema.cluster_info 也能给出 PD 实例地址。
type PDDiscovery struct {
	// Endpoints 归一化后的 PD 客户端地址（http://host:2379），已去重排序。
	Endpoints []string `json:"endpoints"`
	// Source 发现来源：tiup_meta / cluster_info / mock。
	Source string `json:"source"`
	// Message 面向用户的来源说明。
	Message string `json:"message"`
}

// Primary 返回首个 PD 地址（无结果时为空串）。
func (d *PDDiscovery) Primary() string {
	if d == nil || len(d.Endpoints) == 0 {
		return ""
	}
	return d.Endpoints[0]
}

// PDDiscoverer PD 地址自动发现能力。
//
// 由真实模式（CompositeCollector）与演示模式（MockCollector）分别实现，
// Service 层只依赖本接口，不感知底层用哪条通道。
type PDDiscoverer interface {
	DiscoverPD(ctx context.Context, t *Target) (*PDDiscovery, error)
}

// normalizePDEndpoint 把任意形式的 PD 地址归一化为 http://host:port。
//
// 接受 "10.0.0.11"、"10.0.0.11:2379"、"http://10.0.0.11:2379/"、"https://..."，
// 缺端口补 2379（PD 默认客户端端口）。
func normalizePDEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	scheme := "http"
	switch {
	case strings.HasPrefix(raw, "https://"):
		scheme = "https"
		raw = strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		raw = strings.TrimPrefix(raw, "http://")
	}
	raw = strings.TrimRight(raw, "/")
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		// 只给了主机名/IP：补默认端口。
		return scheme + "://" + net.JoinHostPort(strings.Trim(raw, "[]"), "2379")
	}
	if port == "" {
		port = "2379"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// pdEndpointsFromServers 把 meta.yaml 的 pd_servers 段转换为归一化地址列表。
func pdEndpointsFromServers(servers []MetaServer) []string {
	out := make([]string, 0, len(servers))
	seen := make(map[string]bool, len(servers))
	for _, s := range servers {
		host := strings.TrimSpace(s.Host)
		if host == "" {
			host = strings.TrimSpace(s.IP)
		}
		if host == "" {
			continue
		}
		port := s.Port
		if port <= 0 {
			port = 2379
		}
		ep := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
		if seen[ep] {
			continue
		}
		seen[ep] = true
		out = append(out, ep)
	}
	sort.Strings(out)
	return out
}

// DiscoverPD 真实模式自动发现 PD 地址。
//
// 优先级：
//  1. SSH 读取 TiUP 元数据（meta.yaml 的 pd_servers）——最权威，部署工具即真值来源；
//  2. MySQL 查询 information_schema.cluster_info 中 type='pd' 的实例——TiDB 视角的运行时真值；
//
// 两条通道都不通时返回错误，由上层决定是否降级（PD 为建议项，不影响纳管）。
func (c *CompositeCollector) DiscoverPD(ctx context.Context, t *Target) (*PDDiscovery, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	reasons := make([]string, 0, 2)

	// 通道一：TiUP 元数据。
	if strings.TrimSpace(t.ClusterName) != "" && strings.TrimSpace(t.TiUPHost) != "" {
		meta, err := c.ssh.FetchTiUPMeta(ctx, t)
		if err != nil {
			reasons = append(reasons, "TiUP 元数据读取失败: "+sanitizeCredential(err.Error()))
		} else if eps := pdEndpointsFromServers(meta.PdServers); len(eps) > 0 {
			return &PDDiscovery{
				Endpoints: eps,
				Source:    "tiup_meta",
				Message:   fmt.Sprintf("已从 TiUP 元数据识别 %d 个 PD 节点", len(eps)),
			}, nil
		} else {
			reasons = append(reasons, "TiUP 元数据中无 pd_servers 段")
		}
	} else {
		reasons = append(reasons, "未填写集群名或 TiUP 主机，无法读取 TiUP 元数据")
	}

	// 通道二：information_schema.cluster_info。
	if strings.TrimSpace(t.DBHost) != "" {
		rows, err := c.mysql.QueryMaps(ctx, t, "SELECT instance FROM information_schema.cluster_info WHERE type = 'pd'")
		if err != nil {
			reasons = append(reasons, "cluster_info 查询失败: "+sanitizeCredential(err.Error()))
		} else {
			eps := make([]string, 0, len(rows))
			seen := make(map[string]bool, len(rows))
			for _, row := range rows {
				inst := ""
				for k, v := range row {
					if strings.EqualFold(k, "instance") {
						inst = v
						break
					}
				}
				ep := normalizePDEndpoint(inst)
				if ep == "" || seen[ep] {
					continue
				}
				seen[ep] = true
				eps = append(eps, ep)
			}
			sort.Strings(eps)
			if len(eps) > 0 {
				return &PDDiscovery{
					Endpoints: eps,
					Source:    "cluster_info",
					Message:   fmt.Sprintf("已从 TiDB 的 information_schema.cluster_info 识别 %d 个 PD 节点", len(eps)),
				}, nil
			}
			reasons = append(reasons, "cluster_info 中无 PD 实例记录")
		}
	} else {
		reasons = append(reasons, "未填写数据库地址，无法查询 cluster_info")
	}

	return nil, fmt.Errorf("未能自动发现 PD 地址（%s）", strings.Join(reasons, "；"))
}

// DiscoverPD 演示模式：从内置数据集里取 PD 实例地址，保证无真实集群时「自动获取」也可用。
//
// PD 客户端地址统一用 2379（真实 TiUP 集群中所有 PD 节点的 client port 都是 2379，
// 演示数据集为了实例唯一性给每个节点递增了端口，此处按真实语义归一化）。
func (m *MockCollector) DiscoverPD(_ context.Context, t *Target) (*PDDiscovery, error) {
	f := m.fixture(t.ClusterName)
	servers := make([]MetaServer, 0, 3)
	for _, inst := range f.instanceSet {
		if inst.Component == "pd" {
			servers = append(servers, MetaServer{Host: inst.HostIP, Port: 2379})
		}
	}
	eps := pdEndpointsFromServers(servers)
	if len(eps) == 0 {
		return nil, fmt.Errorf("演示数据集缺少 PD 实例")
	}
	return &PDDiscovery{
		Endpoints: eps,
		Source:    "mock",
		Message:   fmt.Sprintf("演示模式：从内置数据集识别 %d 个 PD 节点", len(eps)),
	}, nil
}
