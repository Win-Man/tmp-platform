// Package addrmap 提供「集群内网平面地址 → TMP 可达地址」的映射能力。
//
// 背景：TiDB 集群可能部署在多个网络平面。例如部署时各节点用的是心跳网络
// 10.10.9.x，而 TMP 与集群之间只有业务网络 158.1.9.x 互通。此时从 meta.yaml
// 与 information_schema 采集到的实例 IP、PD 地址、Grafana / Dashboard 地址
// 都是 10.10.9.x，页面上的链接点不开、SSH 也连不上。
//
// 本包只做纯计算：给定一组规则，把地址串里的 IP 部分替换掉，保留协议头、
// 端口、路径与查询串。规则匹配遵循「最长前缀优先」，因此可以先用网段规则
// 兜底，再对个别机器补精确 IP 规则。
//
// 三条约定：
//  1. 只处理 IPv4。IPv6 一律原样返回，不做猜测。
//  2. 解析失败（如主机名、非法字面量）不报错，原样返回，避免展示层因此中断。
//  3. 规则不参与落库改写：采集每次都写原始地址，映射只作用于「读」与「连接」。
package addrmap

import (
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Rule 一条映射规则。
//
// Source 支持三种写法：
//   - 精确 IP：10.10.9.11
//   - CIDR：10.10.9.0/24
//   - 简写前缀：10.10.9.（等价 10.10.9.0/24）、10.10.（等价 10.10.0.0/16）
//
// Target 必须与 Source 同形态：精确 IP 对精确 IP，网段对同长度网段。
type Rule struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type ruleKind int

const (
	kindIP ruleKind = iota
	kindNet
)

// parsedRule 是校验并规范化之后的规则。
type parsedRule struct {
	kind   ruleKind
	plen   int
	srcIP  net.IP
	dstIP  net.IP
	netw   *net.IPNet
	srcNum uint32
	dstNum uint32
	// Display 规范化后的源/目标，用于界面回显与预览命中说明。
	Display Rule
}

// Mapper 一组规则构成的映射器。rules 已按前缀长度降序排列。
//
// 零值（nil）与空规则集都表示「不做任何改写」，所有方法均可安全调用。
type Mapper struct {
	rules []parsedRule
}

// New 构造映射器。非法规则会被跳过（校验在 NormalizeRule 阶段完成），
// 以保证一条坏规则不会让整个集群的页面无法渲染。
func New(rules []Rule) *Mapper {
	m := &Mapper{rules: make([]parsedRule, 0, len(rules))}
	for _, r := range rules {
		p, err := parseRule(r)
		if err != nil {
			continue
		}
		m.rules = append(m.rules, p)
	}
	// 最长前缀优先：精确 IP 视作 /32，天然排在最前。
	sort.SliceStable(m.rules, func(i, j int) bool { return m.rules[i].plen > m.rules[j].plen })
	return m
}

// Empty 报告是否存在可用规则。
func (m *Mapper) Empty() bool { return m == nil || len(m.rules) == 0 }

// MapIP 映射一个纯 IP 字面量。
func (m *Mapper) MapIP(ipStr string) (string, bool) {
	if m.Empty() {
		return "", false
	}
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return "", false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", false
	}
	r, ok := m.lookup(ip4)
	if !ok {
		return "", false
	}
	return r.apply(ip4), true
}

// MapString 映射「IP + 可选端口 + 可选协议/路径」的地址串。
//
// 覆盖采集数据的真实形态：`10.10.9.11`、`10.10.9.11:2379`、
// `http://10.10.9.11:2379/dashboard`。未命中规则或不是 IP 字面量时原样返回。
func (m *Mapper) MapString(raw string) string {
	if m.Empty() {
		return raw
	}
	s := strings.TrimSpace(raw)
	if s == "" || s == "-" || s == "—" {
		return raw
	}
	scheme, rest := splitScheme(s)
	hostport, tail := splitTail(rest)
	host, port := splitHostPort(hostport)
	if host == "" {
		return raw
	}
	mapped, ok := m.MapIP(host)
	if !ok {
		return raw
	}
	if port != "" {
		return scheme + mapped + ":" + port + tail
	}
	return scheme + mapped + tail
}

// MapStrings 批量映射，保持顺序。
func (m *Mapper) MapStrings(list []string) []string {
	if m.Empty() || len(list) == 0 {
		return list
	}
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = m.MapString(s)
	}
	return out
}

// Explain 返回命中的规则与映射结果，供「试算」接口使用。
// hit 为 false 表示该地址未被任何规则覆盖。
func (m *Mapper) Explain(raw string) (mapped string, hit Rule, ok bool) {
	if m.Empty() {
		return raw, Rule{}, false
	}
	s := strings.TrimSpace(raw)
	scheme, rest := splitScheme(s)
	hostport, tail := splitTail(rest)
	host, port := splitHostPort(hostport)

	ip := net.ParseIP(host)
	if ip == nil {
		return raw, Rule{}, false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return raw, Rule{}, false
	}
	r, found := m.lookup(ip4)
	if !found {
		return raw, Rule{}, false
	}
	out := r.apply(ip4)
	if port != "" {
		out = scheme + out + ":" + port + tail
	} else {
		out = scheme + out + tail
	}
	return out, r.Display, true
}

func (m *Mapper) lookup(ip4 net.IP) (*parsedRule, bool) {
	v := binary.BigEndian.Uint32(ip4)
	for i := range m.rules {
		r := &m.rules[i]
		switch r.kind {
		case kindIP:
			if r.srcNum == v {
				return r, true
			}
		case kindNet:
			if r.netw.Contains(ip4) {
				return r, true
			}
		}
	}
	return nil, false
}

// apply 计算映射结果：网段规则保留源地址在网段内的偏移量。
func (r *parsedRule) apply(ip4 net.IP) string {
	if r.kind == kindIP {
		return r.dstIP.String()
	}
	v := binary.BigEndian.Uint32(ip4)
	return uint32ToIP(r.dstNum + (v - r.srcNum)).String()
}

// ---------------------------------------------------------------------------
// 解析与校验
// ---------------------------------------------------------------------------

// NormalizeRule 校验一条规则并返回规范化形式。
//
// 规范化把简写前缀补全成 CIDR（10.10.9. → 10.10.9.0/24），
// 使存储与展示口径统一，也便于按源地址判重。
func NormalizeRule(source, target string) (Rule, error) {
	src := strings.TrimSpace(source)
	dst := strings.TrimSpace(target)
	if src == "" {
		return Rule{}, fmt.Errorf("源地址不能为空")
	}
	if dst == "" {
		return Rule{}, fmt.Errorf("目标地址不能为空")
	}
	p, err := parseRule(Rule{Source: src, Target: dst})
	if err != nil {
		return Rule{}, err
	}
	return p.Display, nil
}

func parseRule(r Rule) (parsedRule, error) {
	srcKind, srcCIDR, srcIP, srcPlen, err := parseAddr(r.Source, "源地址")
	if err != nil {
		return parsedRule{}, err
	}
	dstKind, _, dstIP, dstPlen, derr := parseAddr(r.Target, "目标地址")
	if derr != nil {
		return parsedRule{}, derr
	}
	if srcKind != dstKind {
		if srcKind == kindNet {
			return parsedRule{}, fmt.Errorf("源是网段，目标也必须是同长度网段（如 158.1.9.0/24）")
		}
		return parsedRule{}, fmt.Errorf("源是单个 IP，目标也必须是单个 IP")
	}
	if srcKind == kindNet && srcPlen != dstPlen {
		return parsedRule{}, fmt.Errorf("源网段前缀长度 /%d 与目标 /%d 不一致，需保持同长度才能按偏移平移", srcPlen, dstPlen)
	}

	p := parsedRule{
		kind:   srcKind,
		plen:   srcPlen,
		srcIP:  srcIP,
		dstIP:  dstIP,
		srcNum: binary.BigEndian.Uint32(srcIP.To4()),
		dstNum: binary.BigEndian.Uint32(dstIP.To4()),
		Display: Rule{
			Source: normalizeText(r.Source, srcCIDR),
			Target: normalizeText(r.Target, nil),
		},
	}
	if srcKind == kindNet {
		p.netw = srcCIDR
		p.Display.Target = dstCIDRString(dstPlen, dstIP)
	}
	if p.srcNum == p.dstNum {
		return parsedRule{}, fmt.Errorf("源地址与目标地址相同，规则不会产生任何效果")
	}
	return p, nil
}

// parseAddr 解析单个地址。返回 kind、CIDR 网络、IP、前缀长度。
func parseAddr(raw, label string) (ruleKind, *net.IPNet, net.IP, int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, nil, nil, 0, fmt.Errorf("%s不能为空", label)
	}
	if !strings.Contains(s, "/") && strings.HasSuffix(s, ".") {
		cidr, ok := prefixToCIDR(s)
		if !ok {
			return 0, nil, nil, 0, fmt.Errorf("%s %q 前缀写法不合法（示例：10.10.9.）", label, raw)
		}
		s = cidr
	}
	if strings.Contains(s, "/") {
		ip, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return 0, nil, nil, 0, fmt.Errorf("%s %q 不是合法网段（示例：10.10.9.0/24）", label, raw)
		}
		ip4 := ip.To4()
		if ip4 == nil {
			return 0, nil, nil, 0, fmt.Errorf("%s %q 仅支持 IPv4", label, raw)
		}
		if !ip4.Equal(ipnet.IP) {
			// 允许 10.10.9.11/24 这种「带主机位的网段」写法，按网络地址归一。
			ipnet = &net.IPNet{IP: ipnet.IP, Mask: ipnet.Mask}
		}
		ones, _ := ipnet.Mask.Size()
		return kindNet, ipnet, ipnet.IP.To4(), ones, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return 0, nil, nil, 0, fmt.Errorf("%s %q 不是合法 IP（示例：10.10.9.11、10.10.9.0/24、10.10.9.）", label, raw)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, nil, nil, 0, fmt.Errorf("%s %q 仅支持 IPv4", label, raw)
	}
	return kindIP, nil, ip4, 32, nil
}

// prefixToCIDR 把 10.10.9. 这类简写补成 10.10.9.0/24。
func prefixToCIDR(s string) (string, bool) {
	body := strings.TrimSuffix(strings.TrimSpace(s), ".")
	if body == "" {
		return "", false
	}
	parts := strings.Split(body, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return "", false
	}
	octets := make([]int, 0, 4)
	for _, p := range parts {
		if p == "" {
			return "", false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return "", false
		}
		octets = append(octets, n)
	}
	plen := len(octets) * 8
	for len(octets) < 4 {
		octets = append(octets, 0)
	}
	return fmt.Sprintf("%d.%d.%d.%d/%d", octets[0], octets[1], octets[2], octets[3], plen), true
}

func normalizeText(raw string, ipnet *net.IPNet) string {
	s := strings.TrimSpace(raw)
	if ipnet != nil {
		ones, _ := ipnet.Mask.Size()
		return dstCIDRString(ones, ipnet.IP.To4())
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String()
		}
	}
	if cidr, ok := prefixToCIDR(s); ok && strings.HasSuffix(s, ".") {
		return cidr
	}
	return s
}

// dstCIDRString 输出规范化网段：网络地址 + 前缀长度。
func dstCIDRString(plen int, ip net.IP) string {
	if ip == nil {
		return ""
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return ip.String()
	}
	mask := net.CIDRMask(plen, 32)
	base := &net.IPNet{IP: ip4.Mask(mask), Mask: mask}
	return fmt.Sprintf("%s/%d", base.IP.String(), plen)
}

func uint32ToIP(v uint32) net.IP {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return net.IP(b)
}

// ---------------------------------------------------------------------------
// 字符串拆解
// ---------------------------------------------------------------------------

// splitScheme 拆出 http:// 这类协议头。
func splitScheme(s string) (scheme, rest string) {
	if idx := strings.Index(s, "://"); idx > 0 {
		head := s[:idx]
		// 协议头里不应出现 / ? # 等路径字符，否则视为路径的一部分。
		if !strings.ContainsAny(head, "/?#") {
			return s[:idx+3], s[idx+3:]
		}
	}
	return "", s
}

// splitTail 拆出路径 / 查询 / 片段。
func splitTail(s string) (head, tail string) {
	if idx := strings.IndexAny(s, "/?#"); idx >= 0 {
		return s[:idx], s[idx:]
	}
	return s, ""
}

// splitHostPort 拆出主机与端口；无端口时 port 为空。
//
// 这里不用 net.SplitHostPort 的返回值判断，因为裸 IP（无端口）会让它报错，
// 而带端口时还需要额外担心 IPv6 方括号，所以先按字面量判断。
func splitHostPort(s string) (host, port string) {
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "[") {
		// IPv6 字面量：交给标准库，取不到就整体当主机（后续 ParseIP 会拒绝）。
		if h, p, err := net.SplitHostPort(s); err == nil {
			return h, p
		}
		return s, ""
	}
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		h, p := s[:idx], s[idx+1:]
		if h != "" && p != "" && !strings.Contains(p, ":") {
			return h, p
		}
	}
	return s, ""
}
