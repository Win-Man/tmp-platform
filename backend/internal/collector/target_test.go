package collector

import (
	"strings"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/model"
)

// 主密钥换过之后，密文还在、密码解不开——必须明确报「凭据不可用」，
// 不能静默置空退化成「缺少凭据 / using password: NO」。
func TestNewTargetCredentialDecryptFailure(t *testing.T) {
	writer, err := crypto.New([]byte("key-before-rotate"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := crypto.New([]byte("key-after-rotate"))
	if err != nil {
		t.Fatal(err)
	}
	cred := &model.ClusterCredential{}
	if cred.SSHPasswordEnc, err = writer.Encrypt("ssh-pass"); err != nil {
		t.Fatal(err)
	}
	if cred.DBPasswordEnc, err = writer.Encrypt("db-pass"); err != nil {
		t.Fatal(err)
	}
	cluster := &model.Cluster{
		Name:     "tidb-demo",
		TiUPHost: "10.0.0.1",
		TiUPUser: "tidb",
		DBHost:   "10.0.0.2",
	}

	t.Run("密钥不一致", func(t *testing.T) {
		tg := NewTarget(cluster, cred, reader)
		if tg.TiUPPassword != "" || tg.DBPassword != "" {
			t.Fatalf("解密失败时不应产出密码: ssh=%q db=%q", tg.TiUPPassword, tg.DBPassword)
		}
		if !strings.Contains(tg.SSHCredIssue, "重新填写") {
			t.Fatalf("SSH 凭据问题描述缺失: %q", tg.SSHCredIssue)
		}
		if !strings.Contains(tg.DBCredIssue, "重新填写") {
			t.Fatalf("数据库凭据问题描述缺失: %q", tg.DBCredIssue)
		}
	})

	t.Run("密钥一致", func(t *testing.T) {
		tg := NewTarget(cluster, cred, writer)
		if tg.TiUPPassword != "ssh-pass" || tg.DBPassword != "db-pass" {
			t.Fatalf("正常解密结果不符: ssh=%q db=%q", tg.TiUPPassword, tg.DBPassword)
		}
		if tg.SSHCredIssue != "" || tg.DBCredIssue != "" {
			t.Fatalf("不该报凭据问题: ssh=%q db=%q", tg.SSHCredIssue, tg.DBCredIssue)
		}
	})

	t.Run("未配置密码不算故障", func(t *testing.T) {
		tg := NewTarget(cluster, &model.ClusterCredential{}, writer)
		if tg.SSHCredIssue != "" || tg.DBCredIssue != "" {
			t.Fatalf("空密文不应报解密失败: ssh=%q db=%q", tg.SSHCredIssue, tg.DBCredIssue)
		}
	})
}

// 凭据解不开时，SSH 通道要在建连之前就把原因抛出来。
func TestSSHChannelDialReportsCredentialIssue(t *testing.T) {
	s := &sshChannel{timeouts: NewCommandTimeouts(30 * time.Second)}
	_, err := s.dial(&Target{
		TiUPHost:     "10.0.0.1",
		TiUPUser:     "tidb",
		SSHCredIssue: "SSH 凭据不可用：已保存的密码/私钥解密失败",
	})
	if err == nil {
		t.Fatal("期望报错，实际成功建连")
	}
	if !strings.Contains(err.Error(), "解密失败") {
		t.Fatalf("错误信息未透出凭据原因: %v", err)
	}
}
