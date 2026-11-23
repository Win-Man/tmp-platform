package collector

import (
	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/model"
)

// NewTarget 由集群配置与解密后的凭据构造采集目标。
//
// cred 可为 nil（尚无凭据）；cipher 为 nil 时不做解密（演示模式）。
func NewTarget(cluster *model.Cluster, cred *model.ClusterCredential, cipher *crypto.Cipher) *Target {
	if cluster == nil {
		return &Target{}
	}
	tiupPort := cluster.TiUPPort
	if tiupPort <= 0 {
		tiupPort = 22
	}
	dbPort := cluster.DBPort
	if dbPort <= 0 {
		dbPort = 4000
	}
	pdHost, pdPort := ParsePDEndpoint(cluster.PDEndpoint)
	t := &Target{
		ClusterID:   cluster.ID,
		ClusterName: cluster.Name,
		TiUPHost:    cluster.TiUPHost,
		TiUPPort:    tiupPort,
		TiUPUser:    cluster.TiUPUser,
		TiUPDir:     cluster.TiUPDir,
		DBHost:      cluster.DBHost,
		DBPort:      dbPort,
		DBUser:      cluster.DBUser,
		PDHost:      pdHost,
		PDPort:      pdPort,
	}
	if cred == nil || cipher == nil {
		return t
	}
	// 解密结果必须区分「没配」和「配了但解不开」：
	// 后者说明主密钥（secret.key）与写入时不一致，任何密码都不再生效，
	// 静默置空只会让下游报「缺少凭据 / using password: NO」误导排查方向。
	if v, err := cipher.Decrypt(cred.SSHPasswordEnc); err == nil {
		t.TiUPPassword = v
	} else if cred.SSHPasswordEnc != "" {
		t.SSHCredIssue = sshCredIssue(err)
	}
	if v, err := cipher.Decrypt(cred.SSHKeyEnc); err == nil {
		t.TiUPKey = v
	} else if cred.SSHKeyEnc != "" {
		t.SSHCredIssue = sshCredIssue(err)
	}
	if v, err := cipher.Decrypt(cred.DBPasswordEnc); err == nil {
		t.DBPassword = v
	} else if cred.DBPasswordEnc != "" {
		t.DBCredIssue = dbCredIssue(err)
	}
	return t
}

// sshCredIssue SSH 凭据不可用提示（可直接展示给用户）。
func sshCredIssue(err error) string {
	return "SSH 凭据不可用：已保存的密码/私钥解密失败，主密钥 secret.key 与写入时不一致，" +
		"请在集群配置里重新填写 SSH 密码或私钥（" + err.Error() + "）"
}

// dbCredIssue 数据库凭据不可用提示（可直接展示给用户）。
func dbCredIssue(err error) string {
	return "数据库凭据不可用：已保存的密码解密失败，主密钥 secret.key 与写入时不一致，" +
		"请在集群配置里重新填写数据库密码（" + err.Error() + "）"
}
