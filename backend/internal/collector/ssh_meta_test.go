package collector

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/pkg/sshcmd"
)

// localRunner 单测专用：用 /bin/sh 本地执行白名单命令。
//
// 生产代码只走 SSH（平台不提供本机直读模式），这里只是让「远端输出解析」
// 这类逻辑在没起 sshd 的开发机上也能验证。
type localRunner struct{ timeout time.Duration }

func (l *localRunner) RunTemplate(ctx context.Context, template string, args ...string) (string, error) {
	cmd, err := sshcmd.Build(template, args...)
	if err != nil {
		return "", err
	}
	cctx := ctx
	if l.timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, l.timeout)
		defer cancel()
	}
	c := exec.CommandContext(cctx, "/bin/sh", "-c", cmd)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return stdout.String(), fmt.Errorf("本地执行失败: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (l *localRunner) Close() error { return nil }

// newLocalChannel 构造注入本地执行器的 SSH 通道（仅单测使用）。
func newLocalChannel(t *testing.T, timeout time.Duration) *sshChannel {
	t.Helper()
	lr := &localRunner{timeout: timeout}
	return &sshChannel{
		timeouts: NewCommandTimeouts(timeout),
		dialer:   func(*Target) (cmdRunner, error) { return lr, nil },
	}
}

// writeFakeTiUP 在 dir/bin/tiup 放一个只输出固定内容的假 tiup。
func writeFakeTiUP(t *testing.T, dir, payload string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	p := filepath.Join(bin, "tiup")
	script := "#!/bin/sh\ncat <<'EOF'\n" + payload + "\nEOF\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("写入假 tiup 失败: %v", err)
	}
	return p
}

// TestFetchTiUPMeta_FallbackToHomeBin 复现真实故障形态：tiup 不在 SSH 非交互
// 会话的 PATH 里，只能靠 $HOME/.tiup/bin/tiup 的绝对路径执行。
//
// 期望：仍能拿到 display JSON，Source=display_json，且命令里用的是绝对路径。
func TestFetchTiUPMeta_FallbackToHomeBin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 POSIX shell")
	}
	payload, err := os.ReadFile("testdata/tiup-display.json")
	if err != nil {
		t.Fatalf("读取样本失败: %v", err)
	}
	home := t.TempDir()
	writeFakeTiUP(t, filepath.Join(home, ".tiup"), string(payload))

	// PATH 里没有 tiup —— 与 ssh host 'tiup ...' 报 command not found 的场景一致。
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")

	ch := newLocalChannel(t, 5*time.Second)
	meta, err := ch.FetchTiUPMeta(context.Background(), &Target{
		TiUPHost:    "127.0.0.1",
		ClusterName: "tidb-demo",
	})
	if err != nil {
		t.Fatalf("应通过 $HOME/.tiup/bin/tiup 拿到元数据，实际报错: %v", err)
	}
	if meta.Source != DisplaySourceJSON {
		t.Fatalf("Source = %q, want %q", meta.Source, DisplaySourceJSON)
	}
	if len(meta.PdServers) == 0 || len(meta.TikvServers) == 0 {
		t.Fatalf("拓扑不完整: pd=%d tikv=%d", len(meta.PdServers), len(meta.TikvServers))
	}
}

// TestFetchTiUPMeta_NoTiUP 找不到 tiup 时直接报「实例信息获取失败」，
// 并且不再尝试 meta.yaml 之类的降级来源。
func TestFetchTiUPMeta_NoTiUP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 POSIX shell")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin")

	ch := newLocalChannel(t, 5*time.Second)
	_, err := ch.FetchTiUPMeta(context.Background(), &Target{
		TiUPHost:    "127.0.0.1",
		ClusterName: "tidb-demo",
	})
	if err == nil {
		t.Fatal("找不到 tiup 时应返回错误")
	}
	if !strings.Contains(err.Error(), "实例信息获取失败") {
		t.Fatalf("错误信息应明确为实例信息获取失败，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "未定位到 tiup") {
		t.Fatalf("错误信息应说明 tiup 未定位到，实际: %v", err)
	}
	if strings.Contains(err.Error(), "meta.yaml") {
		t.Fatalf("不应再出现 meta.yaml 降级路径，实际: %v", err)
	}
}
