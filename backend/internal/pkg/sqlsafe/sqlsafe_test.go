package sqlsafe

import "testing"

// TestPrivilegeAll 锁定 ALL / ALL PRIVILEGES 的归一化与白名单行为。
func TestPrivilegeAll(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"ALL", "ALL PRIVILEGES", false},
		{"all", "ALL PRIVILEGES", false},
		{" all ", "ALL PRIVILEGES", false},
		{"ALL PRIVILEGES", "ALL PRIVILEGES", false},
		{"all privileges", "ALL PRIVILEGES", false},
		{"SELECT", "SELECT", false},
		{"insert", "INSERT", false},
		{" SHOW DATABASES ", "SHOW DATABASES", false},
		{"GRANT OPTION", "", true}, // 不在白名单
		{"SUPER", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := Privilege(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("Privilege(%q) 期望报错，实际返回 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Privilege(%q) 期望成功，实际报错: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("Privilege(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}
