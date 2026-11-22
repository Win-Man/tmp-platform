package sshcmd

import "testing"

// TestBuild_TiUPDisplayTemplates 断言 tiup display 的两个模板能正确构造命令。
//
// 模板用 fmt.Sprintf 逐个替换 %s，因此模板内不得出现其它 % 字符；
// 该用例同时守住「模板里只有一个占位符」的约定。
func TestBuild_TiUPDisplayTemplates(t *testing.T) {
	cases := []struct {
		template string
		arg      string
		want     string
	}{
		{
			template: "tiup_display_json",
			arg:      "tidb-demo",
			want:     "tiup cluster display tidb-demo --format=json",
		},
		{
			template: "tiup_display",
			arg:      "tidb-demo",
			want:     "tiup cluster display tidb-demo",
		},
	}
	for _, tc := range cases {
		got, err := Build(tc.template, tc.arg)
		if err != nil {
			t.Fatalf("Build(%q) 失败: %v", tc.template, err)
		}
		if got != tc.want {
			t.Fatalf("Build(%q) = %q, want %q", tc.template, got, tc.want)
		}
	}
}

// TestBuild_RejectsIllegalArg 断言参数白名单仍然生效。
func TestBuild_RejectsIllegalArg(t *testing.T) {
	if _, err := Build("tiup_display", "evil; rm -rf /"); err == nil {
		t.Fatal("非法参数应被拒绝")
	}
	if _, err := Build("unknown_template"); err == nil {
		t.Fatal("未知模板应被拒绝")
	}
}
