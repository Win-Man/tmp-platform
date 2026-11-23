package collector

import (
	"context"
	"errors"
	"testing"
)

// fakeRunner 记录模板调用并按预设返回，用于验证 tiup 定位逻辑。
type fakeRunner struct {
	out   map[string]string
	err   map[string]error
	calls []string
	args  [][]string
}

func (f *fakeRunner) RunTemplate(ctx context.Context, template string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.calls = append(f.calls, template)
	f.args = append(f.args, args)
	if e, ok := f.err[template]; ok {
		return "", e
	}
	return f.out[template], nil
}

func (f *fakeRunner) Close() error { return nil }

// TestResolveTiUPBin_FromPath 远端 PATH 能直接找到时优先使用它。
func TestResolveTiUPBin_FromPath(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"tiup_probe": "/usr/local/bin/tiup\n"}}
	got, ok := resolveTiUPBin(context.Background(), r, &Target{})
	if !ok || got != "/usr/local/bin/tiup" {
		t.Fatalf("resolveTiUPBin = (%q, %v), want (/usr/local/bin/tiup, true)", got, ok)
	}
	if len(r.calls) != 1 {
		t.Fatalf("PATH 命中后不应继续探测，实际调用 %v", r.calls)
	}
}

// TestResolveTiUPBin_FallbackTiUPDir PATH 与默认目录都不可用时，回落到 <TiUPDir>/bin/tiup。
func TestResolveTiUPBin_FallbackTiUPDir(t *testing.T) {
	r := &fakeRunner{
		err: map[string]error{"tiup_probe": errors.New("not found")},
	}
	got, ok := resolveTiUPBin(context.Background(), r, &Target{TiUPDir: "/opt/tiup/"})
	if !ok || got != "/opt/tiup/bin/tiup" {
		t.Fatalf("resolveTiUPBin = (%q, %v), want (/opt/tiup/bin/tiup, true)", got, ok)
	}
	if len(r.calls) != 2 || r.calls[1] != "tiup_dir" {
		t.Fatalf("应按 tiup_dir 校验存在性，实际调用 %v", r.calls)
	}
}

// TestResolveTiUPBin_NotFound 全部失败时返回裸 tiup，并让调用方标记未找到。
func TestResolveTiUPBin_NotFound(t *testing.T) {
	r := &fakeRunner{
		err: map[string]error{
			"tiup_probe": errors.New("not found"),
			"tiup_dir":   errors.New("No such file"),
		},
	}
	got, ok := resolveTiUPBin(context.Background(), r, &Target{TiUPDir: "/opt/tiup"})
	if ok || got != "tiup" {
		t.Fatalf("resolveTiUPBin = (%q, %v), want (tiup, false)", got, ok)
	}
}

// TestResolveTiUPBin_RejectsWeirdOutput 探测输出不是绝对路径时不能拼进命令。
func TestResolveTiUPBin_RejectsWeirdOutput(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"tiup_probe": "tiup is aliased to /usr/bin/tiup\n"}}
	if _, ok := resolveTiUPBin(context.Background(), r, &Target{}); ok {
		t.Fatal("非绝对路径的探测结果不应被采纳")
	}
}
