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
	if v, err := cipher.Decrypt(cred.SSHPasswordEnc); err == nil {
		t.TiUPPassword = v
	}
	if v, err := cipher.Decrypt(cred.SSHKeyEnc); err == nil {
		t.TiUPKey = v
	}
	if v, err := cipher.Decrypt(cred.DBPasswordEnc); err == nil {
		t.DBPassword = v
	}
	return t
}
