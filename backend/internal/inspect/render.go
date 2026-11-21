package inspect

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/model"
)

// ReportData 报告渲染入参。
type ReportData struct {
	ReportNo     string
	ClusterName  string
	TemplateName string
	Run          *model.InspectRun
	Results      []model.InspectResult
	// Items 巡检项定义，用于补充检查方式与判定表达式。
	Items []model.InspectItem
	// FetchedAt 数据采集时间（PRD F-55 要求报告含采集时间）。
	FetchedAt *time.Time
	Now       time.Time
}

// LevelLabel 分级中文名。
func LevelLabel(level string) string {
	switch level {
	case model.LevelCritical:
		return "严重"
	case model.LevelWarning:
		return "警告"
	case model.LevelPass:
		return "通过"
	case model.LevelNA:
		return "不适用"
	default:
		return "未知"
	}
}

// ConclusionLabel 结论中文名。
func ConclusionLabel(c string) string {
	switch c {
	case "fail":
		return "不通过"
	case "risk":
		return "有风险"
	case "pass":
		return "通过"
	default:
		return "未知"
	}
}

// itemIndex 按 code 建索引。
func itemIndex(items []model.InspectItem) map[string]model.InspectItem {
	out := make(map[string]model.InspectItem, len(items))
	for _, it := range items {
		out[it.Code] = it
	}
	return out
}

// groupByCategory 按分类分组并保持固定顺序。
func groupByCategory(results []model.InspectResult) []struct {
	Category string
	Results  []model.InspectResult
} {
	byCat := map[string][]model.InspectResult{}
	for _, r := range results {
		byCat[r.Category] = append(byCat[r.Category], r)
	}
	out := make([]struct {
		Category string
		Results  []model.InspectResult
	}, 0, len(byCat))
	for _, c := range CategoryOrder {
		if list, ok := byCat[c]; ok {
			out = append(out, struct {
				Category string
				Results  []model.InspectResult
			}{Category: c, Results: list})
		}
	}
	// 未列入固定顺序的分类追加到末尾（按字典序）。
	var extra []string
	for c := range byCat {
		found := false
		for _, known := range CategoryOrder {
			if known == c {
				found = true
				break
			}
		}
		if !found {
			extra = append(extra, c)
		}
	}
	sort.Strings(extra)
	for _, c := range extra {
		out = append(out, struct {
			Category string
			Results  []model.InspectResult
		}{Category: c, Results: byCat[c]})
	}
	return out
}

// suggestions 汇总所有非通过项的修复建议。
func suggestions(results []model.InspectResult) []model.InspectResult {
	var out []model.InspectResult
	for _, r := range results {
		if r.Level == model.LevelCritical || r.Level == model.LevelWarning {
			out = append(out, r)
		}
	}
	return out
}

// RenderMarkdown 渲染 Markdown 报告。
func RenderMarkdown(d *ReportData) string {
	now := d.Now
	if now.IsZero() {
		now = time.Now()
	}
	var b strings.Builder
	b.WriteString("# TiDB 集群巡检报告\n\n")

	run := d.Run
	b.WriteString("| 项 | 内容 |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| 报告号 | %s |\n", dash(d.ReportNo))
	fmt.Fprintf(&b, "| 运行号 | %s |\n", dash(runNo(run)))
	fmt.Fprintf(&b, "| 集群 | %s |\n", dash(d.ClusterName))
	fmt.Fprintf(&b, "| 巡检模板 | %s |\n", dash(d.TemplateName))
	fmt.Fprintf(&b, "| 触发方式 | %s |\n", triggerLabel(run))
	fmt.Fprintf(&b, "| 执行时间 | %s |\n", runTime(run, now))
	fmt.Fprintf(&b, "| 数据采集时间 | %s |\n", fetchedLabel(d.FetchedAt))
	fmt.Fprintf(&b, "| 报告生成时间 | %s |\n", now.Format("2006-01-02 15:04:05 -07:00"))
	if run != nil {
		fmt.Fprintf(&b, "| 通过率 | %.1f%% |\n", run.PassRate)
		fmt.Fprintf(&b, "| 整体结论 | %s |\n", ConclusionLabel(run.Conclusion))
	}
	b.WriteString("\n## 分级统计\n\n")
	if run != nil {
		b.WriteString("| 严重 | 警告 | 通过 | 不适用 | 未知 | 合计 |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
		fmt.Fprintf(&b, "| %d | %d | %d | %d | %d | %d |\n\n",
			run.CriticalCount, run.WarningCount, run.PassCount, run.NACount, run.UnknownCount, run.TotalCount)
	} else {
		b.WriteString("无统计数据。\n\n")
	}

	idx := itemIndex(d.Items)
	groups := groupByCategory(d.Results)
	for _, g := range groups {
		fmt.Fprintf(&b, "## %s（%s）\n\n", CategoryLabel(g.Category), g.Category)
		for _, r := range g.Results {
			fmt.Fprintf(&b, "### [%s] %s %s\n\n", LevelLabel(r.Level), r.ItemCode, r.ItemName)
			b.WriteString("| 项 | 内容 |\n| --- | --- |\n")
			if it, ok := idx[r.ItemCode]; ok {
				fmt.Fprintf(&b, "| 检查方式 | %s |\n", dash(it.CheckMethod))
				fmt.Fprintf(&b, "| 判定表达式 | `%s` |\n", dash(it.Expr))
			}
			fmt.Fprintf(&b, "| 判定结果 | %s |\n", LevelLabel(r.Level))
			fmt.Fprintf(&b, "| 实际值 | %s |\n", dash(r.ActualValue))
			fmt.Fprintf(&b, "| 期望值 | %s |\n", dash(r.ExpectedValue))
			fmt.Fprintf(&b, "| 证据 | %s |\n", dash(r.Evidence))
			fmt.Fprintf(&b, "| 修复建议 | %s |\n", dash(r.Suggestion))
			b.WriteString("\n")
		}
	}

	b.WriteString("## 修复建议汇总\n\n")
	sug := suggestions(d.Results)
	if len(sug) == 0 {
		b.WriteString("本次巡检未发现需要处理的严重或警告项。\n")
	} else {
		for i, r := range sug {
			fmt.Fprintf(&b, "%d. **[%s] %s %s**：%s\n", i+1, LevelLabel(r.Level), r.ItemCode, r.ItemName, dash(r.Suggestion))
		}
	}
	b.WriteString("\n---\n\n> 本报告由 TMP（TiDB Manage Platform）自动生成，判定规则基于内置最佳实践模板，仅供参考。\n")
	return b.String()
}

// RenderHTML 渲染自包含 HTML 报告（内联样式、无外部依赖、可离线打开）。
func RenderHTML(d *ReportData) string {
	now := d.Now
	if now.IsZero() {
		now = time.Now()
	}
	run := d.Run
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>巡检报告 %s - %s</title>\n", esc(d.ReportNo), esc(d.ClusterName))
	b.WriteString(`<style>
:root{--primary:#2563EB;--success:#16A34A;--warning:#D97706;--danger:#DC2626;--muted:#94A3B8;--border:#E5E7EB;--text:#1F2733;}
*{box-sizing:border-box}
body{margin:0;padding:32px;background:#F5F7FA;color:var(--text);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;font-size:14px;line-height:1.7;}
.wrap{max-width:1080px;margin:0 auto;background:#fff;border-radius:8px;padding:32px;box-shadow:0 1px 4px rgba(15,23,42,.08);}
h1{font-size:22px;margin:0 0 8px;color:var(--text)}
h2{font-size:17px;margin:32px 0 12px;padding-left:10px;border-left:3px solid var(--primary)}
h3{font-size:15px;margin:20px 0 8px}
table{width:100%;border-collapse:collapse;margin:8px 0 16px;font-size:13px}
th,td{border:1px solid var(--border);padding:8px 10px;text-align:left;vertical-align:top}
th{background:#F8FAFC;font-weight:600;white-space:nowrap;width:140px}
.sub{color:#64748B;font-size:13px;margin-bottom:24px}
.tag{display:inline-block;padding:1px 8px;border-radius:10px;font-size:12px;line-height:20px;color:#fff}
.tag-critical{background:var(--danger)}
.tag-warning{background:var(--warning)}
.tag-pass{background:var(--success)}
.tag-na,.tag-unknown{background:var(--muted)}
.cards{display:flex;gap:12px;flex-wrap:wrap;margin:12px 0 4px}
.card{flex:1 1 120px;border:1px solid var(--border);border-radius:6px;padding:12px 16px;background:#F8FAFC}
.card .k{color:#64748B;font-size:12px}
.card .v{font-size:20px;font-weight:600;margin-top:2px}
.code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;background:#F1F5F9;padding:1px 5px;border-radius:4px;font-size:12px}
.footer{margin-top:32px;padding-top:16px;border-top:1px solid var(--border);color:#64748B;font-size:12px}
ol{padding-left:22px}
</style>
</head>
<body>
<div class="wrap">
`)
	fmt.Fprintf(&b, "<h1>TiDB 集群巡检报告</h1>\n")
	fmt.Fprintf(&b, "<div class=\"sub\">%s · %s · 报告号 %s</div>\n", esc(d.ClusterName), esc(ConclusionLabel(conclusionOf(run))), esc(d.ReportNo))

	b.WriteString("<div class=\"cards\">\n")
	writeCard := func(k, v string) {
		fmt.Fprintf(&b, "<div class=\"card\"><div class=\"k\">%s</div><div class=\"v\">%s</div></div>\n", esc(k), esc(v))
	}
	writeCard("通过率", fmt.Sprintf("%.1f%%", passRateOf(run)))
	writeCard("巡检项总数", fmt.Sprintf("%d", totalOf(run)))
	writeCard("严重", fmt.Sprintf("%d", criticalOf(run)))
	writeCard("警告", fmt.Sprintf("%d", warningOf(run)))
	b.WriteString("</div>\n")

	b.WriteString("<h2>基本信息</h2>\n<table>\n")
	row := func(k, v string) {
		fmt.Fprintf(&b, "<tr><th>%s</th><td>%s</td></tr>\n", esc(k), v)
	}
	row("报告号", esc(d.ReportNo))
	row("运行号", esc(runNo(run)))
	row("集群", esc(d.ClusterName))
	row("巡检模板", esc(d.TemplateName))
	row("触发方式", esc(triggerLabel(run)))
	row("执行时间", esc(runTime(run, now)))
	row("数据采集时间", esc(fetchedLabel(d.FetchedAt)))
	row("报告生成时间", esc(now.Format("2006-01-02 15:04:05 -07:00")))
	row("整体结论", esc(ConclusionLabel(conclusionOf(run))))
	b.WriteString("</table>\n")

	b.WriteString("<h2>分级统计</h2>\n<table>\n<tr><th>严重</th><th>警告</th><th>通过</th><th>不适用</th><th>未知</th><th>合计</th></tr>\n")
	fmt.Fprintf(&b, "<tr><td>%d</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td></tr>\n</table>\n",
		criticalOf(run), warningOf(run), passOf(run), naOf(run), unknownOf(run), totalOf(run))

	idx := itemIndex(d.Items)
	groups := groupByCategory(d.Results)
	for _, g := range groups {
		fmt.Fprintf(&b, "<h2>%s</h2>\n", esc(CategoryLabel(g.Category)))
		for _, r := range g.Results {
			fmt.Fprintf(&b, "<h3><span class=\"tag tag-%s\">%s</span> %s %s</h3>\n",
				esc(r.Level), esc(LevelLabel(r.Level)), esc(r.ItemCode), esc(r.ItemName))
			b.WriteString("<table>\n")
			if it, ok := idx[r.ItemCode]; ok {
				row("检查方式", esc(it.CheckMethod))
				row("判定表达式", "<span class=\"code\">"+esc(it.Expr)+"</span>")
			}
			row("判定结果", "<span class=\"tag tag-"+esc(r.Level)+"\">"+esc(LevelLabel(r.Level))+"</span>")
			row("实际值", esc(dash(r.ActualValue)))
			row("期望值", esc(dash(r.ExpectedValue)))
			row("证据", esc(dash(r.Evidence)))
			row("修复建议", esc(dash(r.Suggestion)))
			b.WriteString("</table>\n")
		}
	}

	b.WriteString("<h2>修复建议汇总</h2>\n")
	sug := suggestions(d.Results)
	if len(sug) == 0 {
		b.WriteString("<p>本次巡检未发现需要处理的严重或警告项。</p>\n")
	} else {
		b.WriteString("<ol>\n")
		for _, r := range sug {
			fmt.Fprintf(&b, "<li><strong>[%s] %s %s</strong>：%s</li>\n",
				esc(LevelLabel(r.Level)), esc(r.ItemCode), esc(r.ItemName), esc(dash(r.Suggestion)))
		}
		b.WriteString("</ol>\n")
	}

	b.WriteString("<div class=\"footer\">本报告由 TMP（TiDB Manage Platform）自动生成，判定规则基于内置最佳实践模板，仅供参考。</div>\n")
	b.WriteString("</div>\n</body>\n</html>\n")
	return b.String()
}

// ---------- 小工具 ----------

// esc HTML 转义。
func esc(s string) string { return html.EscapeString(s) }

// dash 空值统一显示为 —。
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// runNo 取运行号。
func runNo(run *model.InspectRun) string {
	if run == nil {
		return ""
	}
	return run.RunNo
}

// triggerLabel 触发方式中文名。
func triggerLabel(run *model.InspectRun) string {
	if run == nil {
		return ""
	}
	if run.TriggerType == "scheduled" {
		return "定时"
	}
	return "手动"
}

// runTime 执行时间。
func runTime(run *model.InspectRun, now time.Time) string {
	if run == nil {
		return now.Format("2006-01-02 15:04:05 -07:00")
	}
	if run.StartedAt != nil {
		return run.StartedAt.Format("2006-01-02 15:04:05 -07:00")
	}
	return run.CreatedAt.Format("2006-01-02 15:04:05 -07:00")
}

// fetchedLabel 数据采集时间。
func fetchedLabel(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Format("2006-01-02 15:04:05 -07:00")
}

// conclusionOf 取结论。
func conclusionOf(run *model.InspectRun) string {
	if run == nil {
		return ""
	}
	return run.Conclusion
}

// passRateOf 取通过率。
func passRateOf(run *model.InspectRun) float64 {
	if run == nil {
		return 0
	}
	return run.PassRate
}

// totalOf 取项总数。
func totalOf(run *model.InspectRun) int {
	if run == nil {
		return 0
	}
	return run.TotalCount
}

// criticalOf 取严重数。
func criticalOf(run *model.InspectRun) int {
	if run == nil {
		return 0
	}
	return run.CriticalCount
}

// warningOf 取警告数。
func warningOf(run *model.InspectRun) int {
	if run == nil {
		return 0
	}
	return run.WarningCount
}

// passOf 取通过数。
func passOf(run *model.InspectRun) int {
	if run == nil {
		return 0
	}
	return run.PassCount
}

// naOf 取不适用数。
func naOf(run *model.InspectRun) int {
	if run == nil {
		return 0
	}
	return run.NACount
}

// unknownOf 取未知数。
func unknownOf(run *model.InspectRun) int {
	if run == nil {
		return 0
	}
	return run.UnknownCount
}
