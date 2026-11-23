package collector

import "testing"

// TestSupportsResourceGroupVersion 锁定 Resource Group 的版本判定规则。
//
// 常规门槛是 >= 7.5；但 v7.1.8-5.x 是回移了 Resource Control 的构建分支
// （TiDB Cloud / 企业版发布形态），版本主体偏低却实际支持，不能误判为不支持。
func TestSupportsResourceGroupVersion(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		// 常规版本线
		{"v6.5.0", false},
		{"v7.1.0", false},
		{"v7.1.8", false}, // 纯 7.1.8 没有回移构建号，不支持
		{"v7.5.0", true},
		{"v8.5.1", true},
		{"5.7.25-TiDB-v8.5.1", true},

		// 回移分支：v7.1.8-5.x
		{"v7.1.8-5.1", true},
		{"7.1.8-5.1", true},
		{"5.7.25-TiDB-v7.1.8-5.1", true},
		{"v7.1.8-5.2", true},
		{"v7.1.8-5.10", true},
		{"V7.1.8-5.3", true},

		// 不该被前缀模糊匹配误命中
		{"v7.1.8-4.9", false}, // 构建号 4.x，不在回移清单内
		{"v7.1.9-5.1", false}, // 不同 patch，未被证实回移
		{"v7.1.80-5.1", false},
		{"", false},
		{"unknown", false},
	}
	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			major, minor, ok := ParseVersion(c.version)
			got := ok && supportsRGVersion(c.version, major, minor)
			if got != c.want {
				t.Fatalf("version %q: got %v, want %v", c.version, got, c.want)
			}
			// BuildCapabilities 对外口径必须与之一致。
			if caps := BuildCapabilities(c.version, nil); caps.SupportsResourceGroup != c.want {
				t.Fatalf("version %q: BuildCapabilities.SupportsResourceGroup = %v, want %v",
					c.version, caps.SupportsResourceGroup, c.want)
			}
		})
	}
}

// TestParseVersionTiDBPrefix 锁定「优先取 -TiDB-v 后的版本」的解析规则。
func TestParseVersionTiDBPrefix(t *testing.T) {
	cases := []struct {
		version      string
		major, minor int
		ok           bool
	}{
		{"v7.5.0", 7, 5, true},
		{"8.5.1", 8, 5, true},
		{"5.7.25-TiDB-v7.5.0", 7, 5, true},
		{"5.7.25-TiDB-v7.1.8-5.1", 7, 1, true},
		{"Release Version: v8.5.1", 8, 5, true},
		{"unknown", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := ParseVersion(c.version)
		if ok != c.ok || major != c.major || minor != c.minor {
			t.Fatalf("ParseVersion(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.version, major, minor, ok, c.major, c.minor, c.ok)
		}
	}
}

// TestNormalizeCapabilities 锁定「历史快照按当前支持矩阵校正」的行为。
func TestNormalizeCapabilities(t *testing.T) {
	// nil 快照 → 直接按版本构造。
	if got := NormalizeCapabilities(nil, "v7.1.8-5.1"); got == nil || !got.SupportsResourceGroup {
		t.Fatalf("nil 快照应按版本构造且支持 RG，实际 %+v", got)
	}

	// 老快照（7.1.8-5.1 采集时被误判为不支持）→ 校正为支持。
	stale := &Capabilities{TiDBVersion: "v7.1.8-5.1", SupportsResourceGroup: false}
	if got := NormalizeCapabilities(stale, "v7.1.8-5.1"); !got.SupportsResourceGroup {
		t.Fatal("回移版本的老快照应被校正为支持")
	}

	// 探测真实失败 → 保持不支持，尊重探测结论。
	probed := &Capabilities{
		TiDBVersion:           "v7.1.8-5.1",
		SupportsResourceGroup: false,
		ProbeErrors:           map[string]string{"resource_groups": "permission denied"},
	}
	if got := NormalizeCapabilities(probed, "v7.1.8-5.1"); got.SupportsResourceGroup {
		t.Fatal("探测失败的快照不应被校正为支持")
	}

	// 版本本身不支持 → 不能被快照「抬」成支持。
	if got := NormalizeCapabilities(
		&Capabilities{SupportsResourceGroup: true}, "v7.1.0"); !got.SupportsResourceGroup {
		t.Fatal("版本不支持时不应改变快照已有结论")
	}

	// 已支持 → 原样返回，不重置其他字段。
	same := &Capabilities{SupportsResourceGroup: true, SupportsSessionKill: true}
	if got := NormalizeCapabilities(same, "v8.5.1"); got != same {
		t.Fatal("已支持的快照应原样返回")
	}
}
