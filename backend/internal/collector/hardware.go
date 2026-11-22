package collector

import (
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
)

// 本文件实现「MySQL 通道采集主机硬件信息」的纯函数部分。
//
// 数据来源是 TiDB 自带的两张集群级硬件表（列结构同构：TYPE / INSTANCE / DEVICE_TYPE / DEVICE_NAME / NAME / VALUE）：
//
//	information_schema.cluster_hardware —— 硬件规格（CPU 架构与核数、内存容量、磁盘容量与用量）
//	information_schema.cluster_load     —— 负载（CPU idle、内存 used-percent）
//
// 两张表的字段语义按 TiDB v8.5.5 实测结果实现（见 ARCHITECTURE §6.6.4）：
//
//	cpu:    DEVICE_TYPE='cpu'    NAME='cpu-arch'            VALUE='arm64'
//	        DEVICE_TYPE='cpu'    NAME='cpu-logical-cores'   VALUE='2'
//	        DEVICE_TYPE='cpu'    NAME='cpu-physical-cores'  VALUE='2'
//	memory: DEVICE_TYPE='memory' DEVICE_NAME='memory'  NAME='capacity' VALUE='3661103104'（字节）
//	disk:   DEVICE_TYPE='disk'   DEVICE_NAME='vda5'    NAME='path'     VALUE='/'
//	        DEVICE_TYPE='disk'   DEVICE_NAME='vda5'    NAME='total'    VALUE='52516859904'（字节）
//	        DEVICE_TYPE='disk'   DEVICE_NAME='vda5'    NAME='used'     VALUE='4104007680'（字节）
//	        DEVICE_TYPE='disk'   DEVICE_NAME='vda5'    NAME='used-percent' VALUE='0.078'
//	load:   DEVICE_TYPE='cpu'    DEVICE_NAME='usage'   NAME='idle'         VALUE='0.77'（0~1）
//	        DEVICE_TYPE='memory' DEVICE_NAME='virtual' NAME='used-percent' VALUE='0.80'（0~1）
//	        DEVICE_TYPE='memory' DEVICE_NAME='swap'    NAME='used-percent' VALUE='NaN'（必须容错忽略）
//
// 聚合逻辑写成纯函数（AggregateHostHardware），便于用真实样例行做表驱动测试。

// bytesPerGB 1 GiB 的字节数。
const bytesPerGB = 1024.0 * 1024.0 * 1024.0

// diskUsage 单个挂载点的磁盘用量聚合。
type diskUsage struct {
	// device 设备名（cluster_hardware 的 DEVICE_NAME，如 vda5 / vdb）。
	device string
	// path 挂载点（NAME='path' 的 VALUE，如 / 、/data）。
	path string
	// total / used 字节数。
	total float64
	used  float64
	// usedPercent 0~1 的原始比例；hasUsedPercent 标记该行是否给出。
	usedPercent    float64
	hasUsedPercent bool
}

// hostHardwareAgg 单台主机的硬件聚合中间态。
type hostHardwareAgg struct {
	ip string

	arch          string
	logicalCores  int
	physicalCores int

	memCapacity float64
	memUsed     float64
	// memUsedPercent / hasMemPercent 来自 cluster_hardware 的 memory/used-percent（采样快照）。
	memUsedPercent float64
	hasMemPercent  bool

	disks     map[string]*diskUsage
	diskOrder []string

	// 以下字段来自 cluster_load（瞬时负载），刻意与上面的 hardware 快照值分开存：
	// 两者语义不同，且必须明确谁优先，否则「多个实例的 time 序列」会互相覆盖。
	cpuIdle        float64
	hasCPULoad     bool
	loadMemPercent float64
	hasLoadMemPct  bool
}

// loadSample 单个实例的一条 cluster_load 聚合记录（同一实例的 cpu / memory 行合并到一条）。
type loadSample struct {
	// instance cluster_load 的 INSTANCE 原文（如 10.10.10.10:4000）。
	// 值缺失时退化为主机 IP，保证仍可排序与选取。
	instance string
	// typeName cluster_load 的 TYPE 列（tidb / tikv / pd / tiflash / ...），可能为空。
	typeName string

	cpuIdle   float64
	hasCPU    bool
	memPct    float64
	hasMemPct bool
}

// AggregateHostHardware 把 cluster_hardware / cluster_load 的行集聚合为每台主机的 HostResource。
//
// 入参是 queryMaps 产出的 map 列表（键统一小写），因此既可直接喂真实查询结果，
// 也可在测试里用样例行构造。函数对任何脏数据都容错：解析失败一律按缺省值处理，绝不 panic。
//
// 返回顺序按主机首次出现的顺序稳定排序，便于测试与展示。
func AggregateHostHardware(hw []map[string]string, load []map[string]string) []HostResource {
	aggs := make(map[string]*hostHardwareAgg, len(hw))
	order := make([]string, 0, len(hw))

	get := func(ip string) *hostHardwareAgg {
		if a, ok := aggs[ip]; ok {
			return a
		}
		a := &hostHardwareAgg{ip: ip, disks: map[string]*diskUsage{}}
		aggs[ip] = a
		order = append(order, ip)
		return a
	}

	for _, row := range hw {
		ip := HardwareHostIP(getStr(row, "instance"))
		if SkipHardwareHost(ip) {
			continue
		}
		a := get(ip)
		deviceType := strings.ToLower(strings.TrimSpace(getStr(row, "device_type")))
		deviceName := strings.TrimSpace(getStr(row, "device_name"))
		name := strings.ToLower(strings.TrimSpace(getStr(row, "name")))
		value := strings.TrimSpace(getStr(row, "value"))
		switch deviceType {
		case "cpu":
			switch name {
			case "cpu-arch":
				if v := NormalizeArch(value); v != "" {
					a.arch = v
				}
			case "cpu-logical-cores":
				if v := parseHardwareInt(value); v > 0 {
					a.logicalCores = v
				}
			case "cpu-physical-cores":
				if v := parseHardwareInt(value); v > 0 {
					a.physicalCores = v
				}
			}
		case "memory":
			switch name {
			case "capacity":
				if v := parseHardwareFloat(value); v > 0 {
					a.memCapacity = v
				}
			case "used":
				if v := parseHardwareFloat(value); v > 0 {
					a.memUsed = v
				}
			case "used-percent":
				if v, ok := parseRatio(value); ok {
					a.memUsedPercent = v
					a.hasMemPercent = true
				}
			}
		case "disk":
			key := deviceName
			if key == "" {
				key = name
			}
			if key == "" {
				continue
			}
			d, ok := a.disks[key]
			if !ok {
				d = &diskUsage{device: key}
				a.disks[key] = d
				a.diskOrder = append(a.diskOrder, key)
			}
			switch name {
			case "path":
				if value != "" {
					d.path = value
				}
			case "total":
				if v := parseHardwareFloat(value); v > 0 {
					d.total = v
				}
			case "used":
				if v := parseHardwareFloat(value); v >= 0 {
					d.used = v
				}
			case "used-percent":
				if v, ok := parseRatio(value); ok {
					d.usedPercent = v
					d.hasUsedPercent = true
				}
			}
		}
	}

	// cluster_load：CPU idle → 使用率；内存 virtual used-percent → 使用率。
	//
	// 一台主机上通常有多个实例（2379 / 4000 / 20160 …），各自给出一条 load 记录。
	// 直接「后写覆盖前写」会让结果依赖数据库返回顺序（同一份数据两次采集可能给出
	// 不同使用率），因此先按实例分组，再按确定性优先级挑一条（见 pickLoadSamples）。
	loadByIP := make(map[string]map[string]*loadSample, len(aggs))
	loadIPOrder := make([]string, 0, len(aggs))
	for _, row := range load {
		instance := strings.TrimSpace(getStr(row, "instance"))
		ip := HardwareHostIP(instance)
		if SkipHardwareHost(ip) {
			continue
		}
		if _, ok := aggs[ip]; !ok {
			// load 里有、hardware 里没有的主机（理论上不会发生）：仍然收下，usage 有值。
			get(ip)
		}
		if instance == "" {
			instance = ip
		}
		byInst, ok := loadByIP[ip]
		if !ok {
			byInst = map[string]*loadSample{}
			loadByIP[ip] = byInst
			loadIPOrder = append(loadIPOrder, ip)
		}
		s := byInst[instance]
		if s == nil {
			s = &loadSample{instance: instance}
			byInst[instance] = s
		}
		if t := strings.ToLower(strings.TrimSpace(getStr(row, "type"))); t != "" {
			s.typeName = t
		}
		deviceType := strings.ToLower(strings.TrimSpace(getStr(row, "device_type")))
		deviceName := strings.ToLower(strings.TrimSpace(getStr(row, "device_name")))
		name := strings.ToLower(strings.TrimSpace(getStr(row, "name")))
		value := strings.TrimSpace(getStr(row, "value"))
		switch {
		case deviceType == "cpu" && deviceName == "usage" && name == "idle":
			if v, ok := parseRatio(value); ok {
				s.cpuIdle = v
				s.hasCPU = true
			}
		case deviceType == "memory" && deviceName == "virtual" && name == "used-percent":
			// DEVICE_NAME='swap' 会给出 NaN，故这里只认 virtual。
			if v, ok := parseRatio(value); ok {
				s.memPct = v
				s.hasMemPct = true
			}
		}
	}

	// 逐主机按优先级取负载值：tidb 实例优先，其次 INSTANCE 字典序最小者。
	// 每个指标独立取（同一台实例的 cpu / memory 行可能只有其一），优先级规则相同。
	for _, ip := range loadIPOrder {
		a := aggs[ip]
		if a == nil {
			continue
		}
		for _, s := range pickLoadSamples(loadByIP[ip]) {
			if !a.hasCPULoad && s.hasCPU {
				a.cpuIdle = s.cpuIdle
				a.hasCPULoad = true
			}
			if !a.hasLoadMemPct && s.hasMemPct {
				a.loadMemPercent = s.memPct
				a.hasLoadMemPct = true
			}
		}
	}

	out := make([]HostResource, 0, len(order))
	for _, ip := range order {
		out = append(out, aggs[ip].toHostResource())
	}
	return out
}

// pickLoadSamples 把一台主机上各实例的 load 记录排成**确定性的**优先顺序：
//
//  1. `TYPE='tidb'` 的实例排在最前（TiDB 是业务入口，其所在机器的 CPU/内存负载最能代表
//     该主机的「真实压力」；TiKV/PD 的负载曲线不同，混用会让同一台机器出现两套读数）；
//  2. 其余按 INSTANCE 字符串升序，保证同样输入永远同样输出（不依赖 map 遍历顺序，
//     也不依赖数据库返回顺序）；
//  3. 同类型多条（同一台机器上多个 TiDB 实例）同样按 INSTANCE 升序，取第一个有值的。
//
// TYPE 列缺失（旧版本 / 调用方只给了 device 行）时退化为规则 2。
func pickLoadSamples(byInst map[string]*loadSample) []*loadSample {
	if len(byInst) == 0 {
		return nil
	}
	instances := make([]string, 0, len(byInst))
	for inst := range byInst {
		instances = append(instances, inst)
	}
	// 先排序，后续两次遍历都建立在这个稳定顺序上。
	sort.Strings(instances)

	out := make([]*loadSample, 0, len(instances))
	for _, inst := range instances {
		if s := byInst[inst]; s != nil && s.typeName == "tidb" {
			out = append(out, s)
		}
	}
	for _, inst := range instances {
		if s := byInst[inst]; s != nil && s.typeName != "tidb" {
			out = append(out, s)
		}
	}
	return out
}

// toHostResource 把聚合中间态转成 HostResource。
func (a *hostHardwareAgg) toHostResource() HostResource {
	h := HostResource{IP: a.ip, Arch: a.arch}
	// 核数：逻辑核优先，缺失时退物理核。
	h.CPUCores = a.logicalCores
	if h.CPUCores <= 0 {
		h.CPUCores = a.physicalCores
	}
	if a.memCapacity > 0 {
		h.MemoryGB = roundHalfUp(a.memCapacity/bytesPerGB, 1)
	}
	// 磁盘：数据盘通常是容量最大的那块，取 total 最大的挂载点代表该主机的磁盘规格。
	if d := a.largestDisk(); d != nil {
		h.DiskTotalGB = roundHalfUp(d.total/bytesPerGB, 1)
		switch {
		case d.hasUsedPercent:
			h.DiskUsagePct = clampPercent(d.usedPercent * 100)
		case d.total > 0:
			h.DiskUsagePct = clampPercent(d.used / d.total * 100)
		}
	}
	// CPU 使用率 = (1 - idle) * 100。
	if a.hasCPULoad {
		h.CPUUsagePct = clampPercent((1 - a.cpuIdle) * 100)
	}
	// 内存使用率优先级：cluster_load 的瞬时 used-percent
	// → cluster_hardware 的 used-percent → capacity/used 反算。
	switch {
	case a.hasLoadMemPct:
		h.MemUsagePct = clampPercent(a.loadMemPercent * 100)
	case a.hasMemPercent:
		h.MemUsagePct = clampPercent(a.memUsedPercent * 100)
	case a.memCapacity > 0 && a.memUsed > 0:
		h.MemUsagePct = clampPercent(a.memUsed / a.memCapacity * 100)
	}
	return h
}

// largestDisk 返回 total 最大的挂载点（相同大小时按设备名稳定排序）。
func (a *hostHardwareAgg) largestDisk() *diskUsage {
	var best *diskUsage
	for _, key := range a.diskOrder {
		d := a.disks[key]
		if d == nil || d.total <= 0 {
			continue
		}
		if best == nil || d.total > best.total {
			best = d
		}
	}
	return best
}

// HardwareHostIP 从 INSTANCE（如 10.10.10.10:4000）解析主机 IP。
//
// 解析失败时原样返回，交给 SkipHardwareHost 判定。
func HardwareHostIP(instance string) string {
	inst := strings.TrimSpace(instance)
	if inst == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(inst); err == nil {
		return strings.TrimSpace(host)
	}
	return inst
}

// SkipHardwareHost 判断主机是否应跳过。
//
// 跳过对象：空值、0.0.0.0（TiDB 在部分版本会给出未绑定的通配地址）、
// :: 与本机回环。回环地址代表「中控机自身」，那条记录由 SSH 通道负责（SSH 更权威），
// 这里跳过可避免 MySQL 通道的近似值覆盖 SSH 的实测值。
func SkipHardwareHost(ip string) bool {
	v := strings.TrimSpace(strings.ToLower(ip))
	switch v {
	case "", "0.0.0.0", "::", "[::]", "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// parseHardwareFloat 容错解析 cluster_hardware / cluster_load 的 VALUE 列。
//
// 兼容：空串、NaN/Inf（TiDB 的 swap used-percent 会给 NaN）、
// 带单位字符串（"16GiB" / "3661103104 bytes" / "1.5G"）。解析失败返回 0。
func parseHardwareFloat(raw string) float64 {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return finiteOrZero(f)
	}
	// 取数字前缀，剩余部分当单位。
	end := 0
	for end < len(v) {
		c := v[end]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0
	}
	num, err := strconv.ParseFloat(v[:end], 64)
	if err != nil {
		return 0
	}
	num = finiteOrZero(num)
	unit := strings.ToUpper(strings.TrimSpace(v[end:]))
	switch {
	case strings.HasPrefix(unit, "T"):
		return num * 1024 * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "G"):
		return num * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "M"):
		return num * 1024 * 1024
	case strings.HasPrefix(unit, "K"):
		return num * 1024
	default:
		return num
	}
}

// parseHardwareInt 容错解析整型字段（核数等）。
func parseHardwareInt(raw string) int {
	return int(math.Round(parseHardwareFloat(raw)))
}

// parseRatio 解析 0~1 的比例值；NaN/Inf/越界一律视为无效。
func parseRatio(raw string) (float64, bool) {
	v := parseHardwareFloat(raw)
	if v < 0 || v > 1 {
		return 0, false
	}
	return v, true
}

// clampPercent 把百分比夹到 0~100 并保留 1 位小数。
func clampPercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v <= 0 {
		return 0
	}
	if v >= 100 {
		return 100
	}
	return roundHalfUp(v, 1)
}

// roundHalfUp 四舍五入到 n 位小数（非负值）。
func roundHalfUp(v float64, n int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	p := math.Pow(10, float64(n))
	return math.Round(v*p) / p
}

// finiteOrZero 过滤 NaN / Inf。
func finiteOrZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
