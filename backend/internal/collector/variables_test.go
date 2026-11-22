package collector

import "testing"

// TestNormalizeScope 锁定 variables_info.VARIABLE_SCOPE 到前端作用域取值的映射。
//
// TiDB 返回的是 "SESSION,GLOBAL" 这种逗号串，前端按 global|session|both 展示，
// 映射错了会让「作用域」列出现难以理解的原始串。
func TestNormalizeScope(t *testing.T) {
	cases := map[string]string{
		"SESSION,GLOBAL": "both",
		"GLOBAL,SESSION": "both",
		"GLOBAL":         "global",
		"SESSION":        "session",
		"global":         "global", // 大小写不敏感
		" session ":      "session",
		"":               "",
		"WEIRD":          "both", // 未知取值按 both 兜底
	}
	for in, want := range cases {
		if got := normalizeScope(in); got != want {
			t.Fatalf("normalizeScope(%q) = %q，期望 %q", in, got, want)
		}
	}
}
