// Package idgen 生成业务号：巡检运行号 IR20261120-0001、报告号 RP20261120-0001。
package idgen

import (
	"fmt"
	"sync"
	"time"
)

// 业务号前缀。
const (
	PrefixInspectRun = "IR"
	PrefixReport     = "RP"
)

// SeqProvider 返回某前缀在某天已使用的最大序号；由 repo 层实现（查库）。
type SeqProvider func(prefix, day string) (int, error)

// Generator 按「前缀 + 日期」维度自增生成业务号。
type Generator struct {
	mu       sync.Mutex
	provider SeqProvider
	// cache 缓存当日已用序号，避免每次都查库。
	cache map[string]int
}

// New 构造生成器；provider 可为 nil（退化为进程内计数）。
func New(provider SeqProvider) *Generator {
	return &Generator{provider: provider, cache: map[string]int{}}
}

// Next 生成下一个业务号，如 IR20261120-0001。
func (g *Generator) Next(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	day := time.Now().Format("20060102")
	key := prefix + day
	seq, ok := g.cache[key]
	if !ok {
		if g.provider != nil {
			if v, err := g.provider(prefix, day); err == nil && v > 0 {
				seq = v
			}
		}
	}
	seq++
	g.cache[key] = seq
	return fmt.Sprintf("%s%s-%04d", prefix, day, seq)
}

// PurgeOtherDays 清理非当日缓存，避免长期运行内存缓慢增长。
func (g *Generator) PurgeOtherDays() {
	day := time.Now().Format("20060102")
	g.mu.Lock()
	defer g.mu.Unlock()
	for k := range g.cache {
		if len(k) < 8 || k[len(k)-8:] != day {
			delete(g.cache, k)
		}
	}
}
