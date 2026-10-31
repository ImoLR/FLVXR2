package model

// LandingExternalNode is one of a user's own nodes outside FLVX (plan 177,
// 「外部节点」), pasted as a share link or imported from a subscription. A
// landing node can send all its traffic out through one of its owner's
// external nodes (「链式代理」, LandingNode.ChainExternalID).
//
// Upstream is the parsed outbound (linechain.Upstream JSON) and holds the
// node's credentials: it is never returned by any API. Protocol / Host / Port
// / Summary are the non-secret parts shown to the owner.
type LandingExternalNode struct {
	ID           int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       int64  `gorm:"not null;index" json:"userId"`
	Name         string `gorm:"not null;default:''" json:"name"`
	Protocol     string `gorm:"not null;default:''" json:"protocol"`
	Host         string `gorm:"not null;default:''" json:"host"`
	Port         int    `gorm:"not null;default:0" json:"port"`
	Summary      string `gorm:"not null;default:''" json:"summary"`
	Upstream     string `gorm:"type:text;not null;default:''" json:"-"`
	UpstreamHash string `gorm:"not null;default:'';index" json:"-"`
	CreatedTime  int64  `gorm:"not null;default:0" json:"createdTime"`
	UpdatedTime  int64  `gorm:"not null;default:0" json:"updatedTime"`
}

func (LandingExternalNode) TableName() string { return "landing_external_node" }

// MaxExternalNodesPerUser caps a non-admin user's external nodes.
const MaxExternalNodesPerUser = 20
