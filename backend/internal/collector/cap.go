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

// ParseVersion 从版本字符串解析 major/minor。
//
// 支持 "v7.5.0"、"7.5.0-TiDB-v7.5.0"、"8.5.1" 等形态。
func ParseVersion(v string) (major, minor int, ok bool) {
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
//   - Resource Group 需 >= 7.5.0；
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
	caps.SupportsResourceGroup = CompareVersion(major, minor, 7, 5) >= 0
	caps.SupportsSessionKill = CompareVersion(major, minor, 6, 5) >= 0
	caps.SupportsPlacementRules = CompareVersion(major, minor, 5, 0) >= 0
	return caps
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
