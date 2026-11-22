package service

import "testing"

// TestRGClause 锁定 Resource Group 名称 / RU / 优先级 / Burstable 的校验与 SQL 片段拼接规则。
//
// RG 名称无法使用占位符，必须先经 sqlsafe.Ident 白名单校验再用反引号包裹；
// RU_PER_SEC 与 PRIORITY 均为白名单枚举，避免注入。
func TestRGClause(t *testing.T) {
	cases := []struct {
		name      string
		ruPerSec  string
		priority  string
		burstable bool
		alter     bool
		want      string
		wantErr   bool
	}{
		// CREATE 场景：burstable=false 省略关键字（默认关闭）。
		{"rg1", "500", "", true, false, "`rg1` RU_PER_SEC = 500 BURSTABLE", false},
		{"rg2", "600", "", false, false, "`rg2` RU_PER_SEC = 600", false},
		{"rg3", "100", "HIGH", false, false, "`rg3` RU_PER_SEC = 100 PRIORITY = HIGH", false},
		{"rg4", "0", "LOW", true, false, "`rg4` RU_PER_SEC = 0 PRIORITY = LOW BURSTABLE", false},
		// ALTER 场景：burstable=false 必须显式输出 BURSTABLE = FALSE，否则增量语义关闭无效。
		{"rg5", "500", "", true, true, "`rg5` RU_PER_SEC = 500 BURSTABLE", false},
		{"rg6", "600", "", false, true, "`rg6` RU_PER_SEC = 600 BURSTABLE = FALSE", false},
		{"rg7", "100", "MEDIUM", false, true, "`rg7` RU_PER_SEC = 100 PRIORITY = MEDIUM BURSTABLE = FALSE", false},
		// 校验失败场景。
		{"bad-name", "100", "", false, false, "", true},                           // 连字符不合法
		{"rg", "abc", "", false, false, "", true},                                 // RU 非数字
		{"rg", "-1", "", false, false, "", true},                                  // 负数不合法
		{"rg", "100", "INVALID", false, false, "", true},                          // 非法优先级
		{"default", "100", "", false, false, "`default` RU_PER_SEC = 100", false}, // default 允许 ALTER（底层不拦截，创建层拦截）
		{"select", "100", "", false, false, "", true},                             // 关键字黑名单
		{"", "100", "", false, false, "", true},                                   // 空名称
	}
	for _, c := range cases {
		t.Run(c.name+"/"+c.ruPerSec+"/"+c.priority, func(t *testing.T) {
			got, err := rgClause(c.name, c.ruPerSec, c.priority, c.burstable, c.alter)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际得到 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望成功，实际报错: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestIsNonNegInt 锁定非负整数校验。
func TestIsNonNegInt(t *testing.T) {
	cases := map[string]bool{
		"0": true, "500": true, "2147483647": true,
		"": false, "-1": false, "1.5": false, "abc": false, " 500": false,
	}
	for in, want := range cases {
		if got := isNonNegInt(in); got != want {
			t.Fatalf("isNonNegInt(%q)=%v, want %v", in, got, want)
		}
	}
}
