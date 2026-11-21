package collector

import (
	"context"
	"sort"
)

// Querier 只读查询能力（Grant 查看等场景），由采集器按模式实现。
type Querier interface {
	Query(ctx context.Context, t *Target, query string, args ...any) ([]map[string]string, error)
}

// Query 真实模式：透传到 MySQL 通道。
func (c *CompositeCollector) Query(ctx context.Context, t *Target, query string, args ...any) ([]map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, ChannelTimeout)
	defer cancel()
	return c.mysql.QueryMaps(ctx, t, query, args...)
}

// Query 演示模式：返回预置只读结果，保证授权页可展示。
func (m *MockCollector) Query(_ context.Context, t *Target, query string, _ ...any) ([]map[string]string, error) {
	lower := ""
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower += string(c)
	}
	if containsAll(lower, "show", "grants") {
		return []map[string]string{
			{"grants for app_order@10.20.%": "GRANT SELECT, INSERT, UPDATE ON *.* TO `app_order`@`10.20.%`"},
			{"grants for app_order@10.20.%": "GRANT SELECT ON `order_db`.* TO `app_order`@`10.20.%`"},
		}, nil
	}
	return []map[string]string{}, nil
}

// containsAll 判断 s 是否包含全部子串。
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// SortedKeys 返回 map 的有序键（证据展示用）。
func SortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
