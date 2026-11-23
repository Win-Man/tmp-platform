package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
)

// 回归：编辑纳管信息时提交的密码必须真的落库。
//
// 前端「编辑纳管信息」弹窗一直带着 sshPassword / dbPassword 提交，但
// UpdateInput 曾经没有这两个字段，JSON 绑定静默丢弃 → 界面提示保存成功、
// 库里密文一字未变。主密钥轮换后用户就是靠这个入口补救，务必锁死。
func TestUpdate_PersistsSubmittedCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update_cred.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.ClusterCredential{}, &model.Host{}, &model.Instance{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	cipher, err := crypto.New([]byte("test-master-key"))
	if err != nil {
		t.Fatalf("构造加密器失败: %v", err)
	}
	creds := repo.NewCredentialRepo(gdb)
	svc := NewClusterService(ClusterDeps{
		Clusters:  repo.NewClusterRepo(gdb),
		Instances: repo.NewInstanceRepo(gdb),
		Hosts:     repo.NewHostRepo(gdb),
		Creds:     creds,
		Cipher:    cipher,
	})
	ctx := context.Background()

	in := draftInput("tidb-demo", "10.0.0.41")
	in.SSHPassword = "old-ssh"
	in.DBPassword = "old-db"
	created, err := svc.Takeover(ctx, in, nil)
	if err != nil {
		t.Fatalf("接管失败: %v", err)
	}
	before, err := creds.GetByClusterID(ctx, created.ClusterID)
	if err != nil {
		t.Fatalf("读取初始凭据失败: %v", err)
	}

	// 只改 SSH 密码：必须生效，且数据库密码原样保留。
	if _, err := svc.Update(ctx, created.ClusterID, UpdateInput{SSHPassword: "new-ssh"}); err != nil {
		t.Fatalf("编辑保存失败: %v", err)
	}
	after, err := creds.GetByClusterID(ctx, created.ClusterID)
	if err != nil {
		t.Fatalf("读取凭据失败: %v", err)
	}
	if after.SSHPasswordEnc == before.SSHPasswordEnc {
		t.Fatal("提交的 SSH 密码没有落库（密文未变化）")
	}
	if pwd, derr := cipher.Decrypt(after.SSHPasswordEnc); derr != nil || pwd != "new-ssh" {
		t.Fatalf("SSH 密码回读不符: pwd=%q err=%v", pwd, derr)
	}
	if after.DBPasswordEnc != before.DBPasswordEnc {
		t.Fatal("只改 SSH 密码不该动数据库密码密文")
	}
	if pwd, derr := cipher.Decrypt(after.DBPasswordEnc); derr != nil || pwd != "old-db" {
		t.Fatalf("数据库密码被破坏: pwd=%q err=%v", pwd, derr)
	}
}

// 主密钥轮换后的补救路径：编辑里重填两个密码，采集目标必须能解出新值。
func TestUpdate_RecoversAfterKeyRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotate.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.ClusterCredential{}, &model.Host{}, &model.Instance{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	oldCipher, err := crypto.New([]byte("key-before"))
	if err != nil {
		t.Fatal(err)
	}
	newCipher, err := crypto.New([]byte("key-after"))
	if err != nil {
		t.Fatal(err)
	}
	creds := repo.NewCredentialRepo(gdb)
	ctx := context.Background()

	in := draftInput("tidb-demo", "10.0.0.42")
	in.SSHPassword = "ssh-pass"
	in.DBPassword = "db-pass"
	oldSvc := NewClusterService(ClusterDeps{Creds: creds, Clusters: repo.NewClusterRepo(gdb), Cipher: oldCipher})
	created, err := oldSvc.Takeover(ctx, in, nil)
	if err != nil {
		t.Fatalf("接管失败: %v", err)
	}

	// 换钥后接管老服务已不可用，用新钥构造服务做「重新填写」。
	newSvc := NewClusterService(ClusterDeps{Creds: creds, Clusters: repo.NewClusterRepo(gdb), Cipher: newCipher})
	if _, err := newSvc.Update(ctx, created.ClusterID, UpdateInput{SSHPassword: "ssh-pass", DBPassword: "db-pass"}); err != nil {
		t.Fatalf("重填凭据失败: %v", err)
	}
	cred, err := creds.GetByClusterID(ctx, created.ClusterID)
	if err != nil {
		t.Fatalf("读取凭据失败: %v", err)
	}
	for name, enc := range map[string]string{"ssh": cred.SSHPasswordEnc, "db": cred.DBPasswordEnc} {
		if _, derr := newCipher.Decrypt(enc); derr != nil {
			t.Fatalf("%s 凭据仍无法用新主密钥解密: %v", name, derr)
		}
	}
}
