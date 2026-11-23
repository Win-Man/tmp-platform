package collector

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// versionRe 提取版本号三段。
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// tidbVersionRe 优先提取 "TiDB-vX.Y" 形态里的 TiDB 自身版本。
//
// TiDB 兼容 MySQL 协议，VERSION() 常见形态是 "5.7.25-TiDB-v7.5.0"：第一个数字段
// 5.7 只是 MySQL 兼容版本，真正代表 TiDB 能力的是 "-TiDB-v" 后面的版本号。
// 不做这层区分会把 v7.5 集群误判成 5.7，从而错误地关闭会话 Kill / Resource Group。
var tidbVersionRe = regexp.MustCompile(`TiDB-v(\d+)\.(\d+)`)

// rgBackportRe 匹配「版本主体低于 7.5、但实际已回移 Resource Control」的构建分支。
//
// TiDB Cloud / 企业版会以 7.1.8-5.x 这类带构建号的形式发布：版本主体仍是 7.1，
// 但已包含 CREATE / ALTER / DROP RESOURCE GROUP。只按 major.minor 比较会把这类
// 版本误判为「不支持」，因此这里按前缀族单独放行；放行后仍由采集阶段的能力探测
// （直接查 information_schema.resource_groups）做最终确认。
//
// 新增回移分支时只需在此追加，不必改动版本比较主逻辑。
var rgBackportRe = regexp.MustCompile(`(?:^|[^0-9])7\.1\.8-5\.[0-9A-Za-z]`)

// supportsRGVersion 判断版本号是否落在 Resource Group 的支持区间。
//
// 常规规则是 >= 7.5；此外 v7.1.8-5.x 这类回移构建同样支持。
func supportsRGVersion(version string, major, minor int) bool {
	if CompareVersion(major, minor, 7, 5) >= 0 {
		return true
	}
	return rgBackportRe.MatchString(strings.TrimSpace(version))
}

// ParseVersion 从版本字符串解析 major/minor。
//
// 支持 "v7.5.0"、"8.5.1"、"5.7.25-TiDB-v7.5.0"、"v7.1.8-5.1" 等形态；
// 含 "-TiDB-v" 时以 TiDB 自身版本为准，而不是前面的 MySQL 兼容版本。
func ParseVersion(v string) (major, minor int, ok bool) {
	if m := tidbVersionRe.FindStringSubmatch(v); len(m) >= 3 {
		major, _ = strconv.Atoi(m[1])
		minor, _ = strconv.Atoi(m[2])
		return major, minor, true
	}
	m := versionRe.FindStringSubmatch(v)
	if len(m) < 3 {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// CompareVersion 按 major/minor 比较：返回 -1/0/1，无法解析返回 0。
func CompareVersion(aMajor, aMinor, bMajor, bMinor int) int {
	if aMajor != bMajor {
		if aMajor < bMajor {
			return -1
		}
		return 1
	}
	if aMinor != bMinor {
		if aMinor < bMinor {
			return -1
		}
		return 1
	}
	return 0
}

// BuildCapabilities 由版本字符串构造能力集合。
//
// 支持矩阵（PRD 9.5）：v6.5 / v7.1 / v7.5 / v8.5。
//   - Resource Group 需 >= 7.5.0，或命中回移分支（如 v7.1.8-5.x）；
//   - 会话 Kill 自 6.5 起全量支持；
//   - Placement Rules 自 5.0 起支持。
func BuildCapabilities(version string, probeErrors map[string]string) *Capabilities {
	major, minor, ok := ParseVersion(version)
	caps := &Capabilities{
		TiDBVersion:            version,
		ProbeErrors:            probeErrors,
		ProbedAt:               time.Now(),
		SupportsSessionKill:    true,
		SupportsPlacementRules: true,
	}
	if !ok {
		// 无法解析版本时保守处理：不暴露高版本能力。
		caps.SupportsSessionKill = false
		caps.SupportsPlacementRules = false
		return caps
	}
	caps.Major, caps.Minor = major, minor
	caps.SupportsResourceGroup = supportsRGVersion(version, major, minor)
	caps.SupportsSessionKill = CompareVersion(major, minor, 6, 5) >= 0
	caps.SupportsPlacementRules = CompareVersion(major, minor, 5, 0) >= 0
	return caps
}

// NormalizeCapabilities 用当前版本支持矩阵校正历史能力快照。
//
// clusters.capabilities 保存的是「上次采集」那一刻的探测结果。支持矩阵更新后
// （例如新增 v7.1.8-5.x 回移分支），老快照会与新规则不一致，用户必须重新采集
// 才能看到已支持的能力。这里做一次定向校正，仅在以下三条同时成立时补回能力：
//  1. 按当前规则该版本支持 Resource Group；
//  2. 快照里标记为不支持；
//  3. 采集阶段没有真实的探测失败记录（否则尊重探测结论）。
func NormalizeCapabilities(stored *Capabilities, version string) *Capabilities {
	if stored == nil {
		return BuildCapabilities(version, nil)
	}
	if stored.SupportsResourceGroup || !BuildCapabilities(version, nil).SupportsResourceGroup {
		return stored
	}
	if stored.ProbeErrors["resource_groups"] != "" {
		return stored
	}
	stored.SupportsResourceGroup = true
	return stored
}

// UnsupportedMessage 生成「版本不支持」的统一提示文案（40901）。
func UnsupportedMessage(feature, version string, needMajor, needMinor int) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "未知版本"
	}
	return fmt.Sprintf("当前 TiDB %s 不支持 %s，需 v%d.%d.0+", v, feature, needMajor, needMinor)
}

// FeatureVisibility 能力 → UI 元素显隐映射。
func FeatureVisibility(caps *Capabilities) map[string]bool {
	if caps == nil {
		return map[string]bool{
			"resourceGroup":  false,
			"sessionKill":    false,
			"placementRules": false,
		}
	}
	return map[string]bool{
		"resourceGroup":  caps.SupportsResourceGroup,
		"sessionKill":    caps.SupportsSessionKill,
		"placementRules": caps.SupportsPlacementRules,
	}
}
