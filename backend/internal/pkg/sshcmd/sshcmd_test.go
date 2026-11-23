package sshcmd

import "testing"

// TestBuild_TiUPDisplayTemplates 断言 tiup display 模板按位置替换「二进制路径 + 集群名」。
func TestBuild_TiUPDisplayTemplates(t *testing.T) {
	cases := []struct {
		template string
		args     []string
		want     string
	}{
		{
			template: "tiup_display_json",
			args:     []string{"/home/tidb/.tiup/bin/tiup", "tidb-demo"},
			want:     "/home/tidb/.tiup/bin/tiup cluster display tidb-demo --format=json",
		},
		{
			template: "tiup_display",
			args:     []string{"/usr/local/bin/tiup", "tidb-demo"},
			want:     "/usr/local/bin/tiup cluster display tidb-demo",
		},
		{
			template: "tiup_dir",
			args:     []string{"/root/.tiup/bin/tiup"},
			want:     "ls -d /root/.tiup/bin/tiup",
		},
	}
	for _, tc := range cases {
		got, err := Build(tc.template, tc.args...)
		if err != nil {
			t.Fatalf("Build(%q) 失败: %v", tc.template, err)
		}
		if got != tc.want {
			t.Fatalf("Build(%q) = %q, want %q", tc.template, got, tc.want)
		}
	}
}

// TestBuild_ProbeTemplate 断言 tiup 探测模板无需参数且同时覆盖 PATH 与默认安装目录。
func TestBuild_ProbeTemplate(t *testing.T) {
	got, err := Build("tiup_probe")
	if err != nil {
		t.Fatalf("Build(tiup_probe) 失败: %v", err)
	}
	want := `command -v tiup 2>/dev/null || ls -d "$HOME/.tiup/bin/tiup" 2>/dev/null`
	if got != want {
		t.Fatalf("Build(tiup_probe) = %q, want %q", got, want)
	}
}

// TestBuild_RejectsIllegalArg 断言参数白名单仍然生效。
func TestBuild_RejectsIllegalArg(t *testing.T) {
	if _, err := Build("tiup_display", "tiup", "evil; rm -rf /"); err == nil {
		t.Fatal("非法参数应被拒绝")
	}
	if _, err := Build("tiup_display_json", "/usr/bin/tiup;sh", "tidb-demo"); err == nil {
		t.Fatal("二进制路径含非法字符应被拒绝")
	}
	if _, err := Build("unknown_template"); err == nil {
		t.Fatal("未知模板应被拒绝")
	}
}
