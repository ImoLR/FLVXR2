package model

// 用户专属域名 (plan 303): one private name per (user, entry node), a CNAME to
// the node's DDNS main domain. A row is one name and is never deleted: a
// retired row is the tombstone that keeps its label from ever being handed
// out again. Portable scalar columns only (SQLite + PostgreSQL).

const (
	UserSubdomainActive  = "active"  // CF record wanted
	UserSubdomainPaused  = "paused"  // CF record deleted, name kept for this user/node
	UserSubdomainRetired = "retired" // CF record deleted, name never used again

	// Pause reasons.
	UserSubdomainReasonExpired  = "expired"  // 到期
	UserSubdomainReasonDisabled = "disabled" // 停用
	UserSubdomainReasonUnused   = "unused"   // round 1 only (no longer set: a name does not pause when its user has no forward on the node)
	UserSubdomainReasonNode     = "node"     // node toggle off or no synced main domain
	// Retire reasons.
	UserSubdomainReasonUserDeleted = "user_deleted"
	UserSubdomainReasonNodeDeleted = "node_deleted"
	UserSubdomainReasonRenamed     = "renamed" // 换新名
	UserSubdomainReasonAdmin       = "admin"   // 删除 by the admin

	// Sync status of the CF side.
	UserSubdomainSyncPending = "pending"
	UserSubdomainSyncSynced  = "synced"
	UserSubdomainSyncFailed  = "failed"
	// Round 3: the record waits for room in the zone (record cap guard); no
	// Cloudflare call is made for it until a pass finds room.
	UserSubdomainSyncCapped = "capped"
)

type UserSubdomain struct {
	ID     int64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID int64 `gorm:"column:user_id;not null;index" json:"userId"`
	// NodeID: the node the CNAME points at now; OriginNodeID: the node the
	// name was made for (改指 changes NodeID only).
	NodeID       int64  `gorm:"column:node_id;not null;index" json:"nodeId"`
	OriginNodeID int64  `gorm:"column:origin_node_id;not null;default:0" json:"originNodeId"`
	Label        string `gorm:"type:varchar(32);not null;uniqueIndex:idx_user_subdomain_label" json:"label"`
	Domain       string `gorm:"type:varchar(253);not null;default:''" json:"domain"`
	ProviderID   int64  `gorm:"column:provider_id;not null;default:0" json:"providerId"`
	ZoneID       string `gorm:"column:zone_id;type:varchar(64);not null;default:''" json:"-"`
	RecordID     string `gorm:"column:record_id;type:varchar(64);not null;default:''" json:"-"`
	// Target: the CNAME content last written ("" = no record).
	Target string `gorm:"type:varchar(253);not null;default:''" json:"target"`
	State  string `gorm:"type:varchar(16);not null;default:'active';index" json:"state"`
	Reason string `gorm:"type:varchar(16);not null;default:''" json:"reason"`
	// Blocked: an admin-deleted tombstone; no new name is made for this
	// user/node until the admin re-allows it (重新分配).
	Blocked     bool   `gorm:"not null;default:false" json:"blocked"`
	Status      string `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	LastError   string `gorm:"column:last_error;type:varchar(255);not null;default:''" json:"lastError"`
	LastSyncAt  int64  `gorm:"column:last_sync_at;type:bigint;not null;default:0" json:"lastSyncAt"`
	FailCount   int    `gorm:"column:fail_count;not null;default:0" json:"failCount"`
	NextRetryAt int64  `gorm:"column:next_retry_at;type:bigint;not null;default:0" json:"nextRetryAt"`
	UnusedSince int64  `gorm:"column:unused_since;type:bigint;not null;default:0" json:"unusedSince"`
	RetiredAt   int64  `gorm:"column:retired_at;type:bigint;not null;default:0" json:"retiredAt"`
	// ReadyAt (round 3): when the record was first verified through the
	// Cloudflare API (0 = never: a fresh name). A fresh name that cannot be
	// created (cap, persistent failure) is not handed out at all; a name that
	// was ready once keeps being shown (the user already has it).
	ReadyAt int64 `gorm:"column:ready_at;type:bigint;not null;default:0" json:"readyAt"`
	// RecordDomain (plan 305): the node DDNS domain this name points at;
	// "" = the node's main domain (plan 303 names, 改指). A node with several
	// DDNS domains (Leikwanhost 沪台IPLC: 电信 -ct main + 联通 -cu extra)
	// gives each user one name per domain.
	RecordDomain string `gorm:"column:record_domain;type:varchar(253);not null;default:''" json:"recordDomain"`
	CreatedTime  int64  `gorm:"column:created_time;type:bigint;not null;default:0" json:"createdTime"`
	UpdatedTime  int64  `gorm:"column:updated_time;type:bigint;not null;default:0" json:"updatedTime"`
}

func (UserSubdomain) TableName() string { return "user_subdomain" }

// 已有转发保持原地址 (plan 305): when the 用户专属域名 toggle of a node goes on
// with this choice, every forward with a port on the node and every native
// landing node on it at that moment is listed here and keeps the address it
// had (no name, subscriptions unchanged) for as long as it exists. Only
// forwards / landings created later get names. RefCreated is the created_time
// of the forward / landing node: a deleted and recreated one is new.
const (
	UserSubdomainKeepForward = "forward"
	UserSubdomainKeepLanding = "landing"
)

type UserSubdomainKeep struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	NodeID      int64  `gorm:"column:node_id;not null;uniqueIndex:idx_user_subdomain_keep,priority:1" json:"nodeId"`
	Kind        string `gorm:"type:varchar(8);not null;uniqueIndex:idx_user_subdomain_keep,priority:2" json:"kind"`
	RefID       int64  `gorm:"column:ref_id;not null;uniqueIndex:idx_user_subdomain_keep,priority:3" json:"refId"`
	RefCreated  int64  `gorm:"column:ref_created;type:bigint;not null;default:0" json:"refCreated"`
	UserID      int64  `gorm:"column:user_id;not null;default:0;index" json:"userId"`
	CreatedTime int64  `gorm:"column:created_time;type:bigint;not null;default:0" json:"createdTime"`
}

func (UserSubdomainKeep) TableName() string { return "user_subdomain_keep" }

// Live: the CF record exists and may be handed out to the user.
func (s UserSubdomain) Live() bool {
	return s.State == UserSubdomainActive && s.Status == UserSubdomainSyncSynced && s.RecordID != ""
}
