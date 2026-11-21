package inspect

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/model"
)

// ProgressFunc 单项判定完成回调（任务引擎据此推进子步骤）。
type ProgressFunc func(done, total int, item model.InspectItem, res model.InspectResult)

// ExecuteRequest 一次巡检执行的入参。
type ExecuteRequest struct {
	Cluster  *model.Cluster
	Snapshot *collector.Snapshot
	Caps     *collector.Capabilities
	Items    []model.InspectItem
	Thresh   Thresholds
	Progress ProgressFunc
	// Canceled 返回 true 时提前终止（在项边界检查）。
	Canceled func() bool
}

// ExecResult 执行统计结果。
type ExecResult struct {
	Results       []model.InspectResult
	Total         int
	CriticalCount int
	WarningCount  int
	PassCount     int
	NACount       int
	UnknownCount  int
	PassRate      float64
	Conclusion    string
}

// Engine 巡检执行器。
type Engine struct{}

// NewEngine 构造执行器。
func NewEngine() *Engine { return &Engine{} }

// Execute 逐项判定；单项失败不中断整份巡检（PRD 8.3）。
func (e *Engine) Execute(ctx context.Context, req ExecuteRequest) (*ExecResult, error) {
	out := &ExecResult{Results: make([]model.InspectResult, 0, len(req.Items))}
	now := time.Now()
	if req.Thresh.CPUPct <= 0 || req.Thresh.DiskPct <= 0 {
		req.Thresh = DefaultThresholds()
	}

	for idx, item := range req.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Canceled != nil && req.Canceled() {
			return nil, ctx.Err()
		}

		start := time.Now()
		res := e.judge(&Input{
			Cluster:  req.Cluster,
			Snapshot: req.Snapshot,
			Caps:     req.Caps,
			Thresh:   req.Thresh,
			Now:      now,
		}, item)
		duration := time.Since(start).Milliseconds()

		expected := res.Expected
		if expected == "" {
			expected = item.Expect
		}
		suggestion := item.Suggestion
		if res.Suggestion != "" {
			suggestion = res.Suggestion
		}
		result := model.InspectResult{
			ItemCode:      item.Code,
			ItemName:      item.Name,
			Category:      item.Category,
			Severity:      item.Severity,
			Level:         res.Level,
			ActualValue:   res.Actual,
			ExpectedValue: expected,
			Evidence:      res.Evidence,
			Suggestion:    suggestion,
			DurationMS:    int(duration),
			CreatedAt:     now,
		}
		out.Results = append(out.Results, result)
		out.Total++

		switch res.Level {
		case model.LevelCritical:
			out.CriticalCount++
		case model.LevelWarning:
			out.WarningCount++
		case model.LevelPass:
			out.PassCount++
		case model.LevelNA:
			out.NACount++
		default:
			out.UnknownCount++
		}

		if req.Progress != nil {
			req.Progress(idx+1, len(req.Items), item, result)
		}
	}

	// 通过率 = 通过 / (严重 + 警告 + 通过)，N/A 与 Unknown 不计入分母。
	denom := out.CriticalCount + out.WarningCount + out.PassCount
	if denom > 0 {
		out.PassRate = math.Round(float64(out.PassCount)/float64(denom)*1000) / 10
	}
	switch {
	case out.CriticalCount > 0:
		out.Conclusion = "fail"
	case out.WarningCount > 0:
		out.Conclusion = "risk"
	default:
		out.Conclusion = "pass"
	}
	return out, nil
}

// judge 执行单项判定，包含 applies 适用性判断与缺失判定函数的降级。
func (e *Engine) judge(in *Input, item model.InspectItem) Result {
	// applies 形如 "min_version:7.5.0"。
	if item.Applies != "" {
		if ok, reason := appliesOK(in, item.Applies); !ok {
			return na(reason)
		}
	}
	checker, ok := Lookup(item.Code)
	if !ok {
		return unknown("未实现判定函数: " + item.Code)
	}
	res := safeRun(checker, in)
	if res.Level == "" {
		return unknown("判定函数未返回结果")
	}
	return res
}

// safeRun 兜底捕获判定函数 panic。
func safeRun(c Checker, in *Input) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			res = unknown(fmt.Sprintf("判定执行异常: %v", r))
		}
	}()
	return c(in)
}

// appliesOK 判断 applies 条件是否满足。
func appliesOK(in *Input, applies string) (bool, string) {
	parts := strings.SplitN(applies, ":", 2)
	if len(parts) != 2 {
		return true, ""
	}
	key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	switch key {
	case "min_version":
		needMajor, needMinor, ok := collector.ParseVersion(val)
		if !ok {
			return true, ""
		}
		version := ""
		if in.Cluster != nil {
			version = in.Cluster.TiDBVersion
		}
		if version == "" && in.Snapshot != nil && in.Snapshot.ClusterInfo != nil {
			version = in.Snapshot.ClusterInfo.TiDBVersion
		}
		if version == "" {
			return false, "未采集到 TiDB 版本，无法判断该项是否适用"
		}
		major, minor, parsed := collector.ParseVersion(version)
		if !parsed {
			return false, "版本号无法解析: " + version
		}
		if collector.CompareVersion(major, minor, needMajor, needMinor) < 0 {
			return false, fmt.Sprintf("当前 TiDB %s 低于 v%d.%d.0，该项不适用", version, needMajor, needMinor)
		}
		return true, ""
	default:
		return true, ""
	}
}
