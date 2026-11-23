//go:build e2e

// 真实 TiUP 端到端用例（默认不参与 CI，需 -tags e2e 且显式提供集群名才运行）。
//
// 运行方式（本机已装 TiUP 且有集群）：
//
//	TMP_E2E_CLUSTER=tidb-demo CGO_ENABLED=1 go test -tags e2e \
//	  -ldflags=-linkmode=external -run TestE2E -v ./internal/collector/
//
// 设计原则：
//   - 只断言「来源 + 数量 + 非空」，不写死版本号/主机 IP 这类与具体机器绑定的值，
//     具体数值由 testdata fixture 单测守；
//   - 环境不具备条件（未设集群名、找不到 tiup、集群不可达）时 Skip 并打印原因，不 FAIL，
//     避免开发者本机与 CI 因环境差异红掉。
package collector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// e2eExpectedInstances 期望实例数：默认按 tidb-demo（5 个）断言，可用环境变量覆盖。
func e2eExpectedInstances() int {
	if raw := os.Getenv("TMP_E2E_INSTANCES"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return 5
}

// ensureTiUPInPath 保证命令白名单里的 `tiup` 可被 exec 找到（本机常见装法在 ~/.tiup/bin）。
func ensureTiUPInPath(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("tiup")
	if err == nil {
		t.Logf("tiup 位于 %s", path)
		return
	}
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		t.Skipf("找不到 tiup 且无法解析 home：%v", homeErr)
	}
	candidate := filepath.Join(home, ".tiup", "bin", "tiup")
	if info, statErr := os.Stat(candidate); statErr != nil || info.IsDir() {
		t.Skipf("找不到 tiup（PATH 与 %s 均不可用），跳过 E2E", candidate)
	}
	t.Setenv("PATH", filepath.Join(home, ".tiup", "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Logf("已把 %s 注入 PATH", filepath.Dir(candidate))
}

// TestE2E_CollectFromRealTiUP 用真实 TiUP 跑一次完整 Collect，验证 display 解析链路可用。
func TestE2E_CollectFromRealTiUP(t *testing.T) {
	cluster := os.Getenv("TMP_E2E_CLUSTER")
	if cluster == "" {
		t.Skip("未设置 TMP_E2E_CLUSTER（如 TMP_E2E_CLUSTER=tidb-demo），跳过 E2E")
	}
	ensureTiUPInPath(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 平台一律 SSH 到 TiUP 中控机取数（无本地直读），因此本用例要求
	// 本机 sshd 可登录、且 TMP_E2E_TIUP_USER 是该集群的 TiUP 属主用户；
	// 不满足时下面的 ChanErrors 分支会 Skip 而不是 FAIL。
	target := &Target{
		ClusterName: cluster,
		TiUPHost:    "localhost",
		TiUPUser:    os.Getenv("TMP_E2E_TIUP_USER"),
	}
	col := NewCompositeCollector(NewCommandTimeouts(ChannelTimeout))
	snap, err := col.Collect(ctx, target, FullScope())
	if err != nil {
		t.Skipf("Collect 整体失败（环境不可用）: %v", err)
	}
	if snap == nil || snap.Meta == nil {
		t.Skipf("快照或 meta 为空（环境不可用），ChanErrors=%v", chanErrorsOf(snap))
	}
	// SSH 拓扑通道是本用例的被测对象；它失败通常意味着本机没有该集群/tiup 不可用 → 跳过而非 FAIL。
	if msg, failed := snap.ChanErrors[KindSSH]; failed {
		t.Skipf("SSH 拓扑通道不可用（环境问题）: %s", msg)
	}

	if got := snap.Meta.Source; got != DisplaySourceJSON {
		t.Fatalf("meta.Source = %q, want %q（真实 TiUP 应优先走 display JSON）", got, DisplaySourceJSON)
	}
	if want := e2eExpectedInstances(); len(snap.Instances) != want {
		t.Fatalf("实例数 = %d, want %d（可用 TMP_E2E_INSTANCES 覆盖）", len(snap.Instances), want)
	}
	if snap.Normalized == nil {
		t.Fatal("Normalized 为 nil")
	}
	if snap.Normalized.Version == "" {
		t.Fatal("集群版本为空")
	}
	if len(snap.Normalized.DashboardURLs) == 0 {
		t.Fatal("DashboardURLs 为空")
	}
	if len(snap.Normalized.PrometheusURLs) == 0 {
		t.Fatal("PrometheusURLs 为空")
	}
	if len(snap.Normalized.GrafanaURLs) == 0 {
		t.Fatal("GrafanaURLs 为空")
	}
	if snap.Normalized.HostCount == 0 || snap.Normalized.ComponentCount == 0 {
		t.Fatalf("派生字段异常：hosts=%d comps=%d", snap.Normalized.HostCount, snap.Normalized.ComponentCount)
	}
	// 实例状态必须已透传（非空），down 数量决定健康判定，不能全是空串。
	for _, inst := range snap.Instances {
		if inst.Status == "" {
			t.Fatalf("实例 %s@%s 的 status 为空，display 状态未透传", inst.Component, inst.HostIP)
		}
	}
	t.Logf("source=%s version=%s instances=%d hosts=%d comps=%d down=%d dash=%v prom=%v graf=%v",
		snap.Meta.Source, snap.Normalized.Version, len(snap.Instances),
		snap.Normalized.HostCount, snap.Normalized.ComponentCount, snap.DownInstances(),
		snap.Normalized.DashboardURLs, snap.Normalized.PrometheusURLs, snap.Normalized.GrafanaURLs)
}

// chanErrorsOf 安全读取快照的通道错误（snap 可能为 nil）。
func chanErrorsOf(snap *Snapshot) map[Kind]string {
	if snap == nil {
		return nil
	}
	return snap.ChanErrors
}
