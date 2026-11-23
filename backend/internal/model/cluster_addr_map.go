package model

import "time"

// ClusterAddrMap 集群地址映射规则：把集群内网平面的地址映射为 TMP 可达地址。
//
// 为什么单独一张表而不是写在 clusters 里：多网络平面下通常是一批机器整体平移
// （10.10.9.0/24 → 158.1.9.0/24），个别机器再补精确 IP 规则，天然是多条记录。
//
// 重要：本表只影响「TMP 视角的读与连接」，不改采集落库的原始地址。
// 采集每次都会把 meta.yaml / information_schema 的原始 IP 写回快照，
// 若在写入端改写，下一轮采集就会被覆盖，规则反而看起来「失效」。
type ClusterAddrMap struct {
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ClusterID int64  `gorm:"column:cluster_id;not null" json:"clusterId"`
	Source    string `gorm:"column:source;type:text;not null" json:"source"`
	Target    string `gorm:"column:target;type:text;not null" json:"target"`
	Note      string `gorm:"column:note;type:text" json:"note"`
	// Enabled 不用 gorm 的 default 标签：带 default 时 GORM 会把 0 当作「未设置」而落成默认值，
	// 导致「新建时就想停用」的规则被静默改成启用。显式赋值更可控。
	Enabled   int       `gorm:"column:enabled;not null" json:"enabled"`
	CreatedBy *int64    `gorm:"column:created_by" json:"createdBy"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (ClusterAddrMap) TableName() string { return "cluster_addr_maps" }
