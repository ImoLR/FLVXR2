package model

// LeiKwanHost (plan 269) is the panel side of the 利群主机接管 of one 利群入口
// machine: the 「面板接管」 and 「流量统计」 switches, the learned per-host
// profile (values that differ from the line/rule defaults), the desired-content
// revision, the latest agent report and the billed traffic counter baseline.
type LeiKwanHost struct {
	ID     int64 `gorm:"primaryKey;autoIncrement" json:"id"`
	NodeID int64 `gorm:"not null;uniqueIndex" json:"nodeId"`
	// Manage 1 = the agent writes the panel's content when it differs.
	Manage int `gorm:"not null;default:0" json:"manage"`
	// Traffic 1 = count the 利群 rule ports on this machine.
	Traffic int `gorm:"not null;default:0" json:"traffic"`
	// Profile: JSON of the learned host profile ('' = not learned yet).
	Profile   string `gorm:"type:text;not null;default:''" json:"-"`
	LearnedAt int64  `gorm:"not null;default:0" json:"learnedAt"`
	// Rev goes up whenever the desired content changes (or 接管 is switched on);
	// Force goes up on 「按面板覆盖」.
	Rev         int64  `gorm:"not null;default:0" json:"rev"`
	Force       int64  `gorm:"not null;default:0" json:"force"`
	DesiredHash string `gorm:"type:varchar(64);not null;default:''" json:"-"`
	// Report: the latest agent report (secret-free JSON).
	Report     string `gorm:"type:text;not null;default:''" json:"-"`
	ReportedAt int64  `gorm:"not null;default:0" json:"reportedAt"`
	SyncedAt   int64  `gorm:"not null;default:0" json:"syncedAt"`
	SyncError  string `gorm:"type:text;not null;default:''" json:"syncError"`
	// Counter baseline of the billed traffic (epoch + port → [up, down]).
	CounterEpoch string `gorm:"type:varchar(32);not null;default:''" json:"-"`
	Counters     string `gorm:"type:text;not null;default:''" json:"-"`
	UpdatedTime  int64  `gorm:"not null;default:0" json:"updatedTime"`
}

func (LeiKwanHost) TableName() string { return "leikwan_host" }
