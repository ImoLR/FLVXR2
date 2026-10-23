package model

// Node DDNS (plan 142). A DNSProvider holds the API credentials of one DNS
// account (Cloudflare only in v1); a NodeDDNSRecord is one A/AAAA name that
// follows an address of a node. Portable scalar columns only (SQLite +
// PostgreSQL); JSON is stored as text.

const (
	DNSProviderCloudflare = "cloudflare"

	DDNSRoleMain  = "main"  // fills the node's IPv4 / IPv6 address field
	DDNSRoleExtra = "extra" // goes into the node's extra IPs (附加 IP)

	DDNSStatusPending = "pending" // waiting for the first / next sync
	DDNSStatusSynced  = "synced"
	DDNSStatusFailed  = "failed"
	DDNSStatusUpgrade = "upgrade" // the node's agent cannot report this address yet
	DDNSStatusNoAddr  = "noaddr"  // the agent reported no address matching the source
)

type DNSProvider struct {
	ID           int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string `gorm:"type:varchar(100);not null" json:"name"`
	ProviderType string `gorm:"column:provider_type;type:varchar(20);not null;default:'cloudflare'" json:"providerType"`
	// Credentials is a JSON object ({"api_token":"…"}); never sent to the UI.
	Credentials    string `gorm:"type:text;not null;default:''" json:"-"`
	TokenStatus    string `gorm:"column:token_status;type:varchar(20);not null;default:''" json:"tokenStatus"`
	TokenExpiresOn int64  `gorm:"column:token_expires_on;type:bigint;not null;default:0" json:"tokenExpiresOn"` // unix ms, 0 = no expiry
	TokenCheckedAt int64  `gorm:"column:token_checked_at;type:bigint;not null;default:0" json:"tokenCheckedAt"`
	TokenError     string `gorm:"column:token_error;type:varchar(255);not null;default:''" json:"tokenError"`
	// Zones is a JSON array of the zone names the token can edit (last test).
	Zones       string `gorm:"type:text;not null;default:'[]'" json:"-"`
	CreatedTime int64  `gorm:"column:created_time;type:bigint;not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"column:updated_time;type:bigint;not null;default:0" json:"updatedTime"`
}

func (DNSProvider) TableName() string { return "dns_provider" }

type NodeDDNSRecord struct {
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	NodeID     int64  `gorm:"column:node_id;not null;index" json:"nodeId"`
	Domain     string `gorm:"type:varchar(253);not null;uniqueIndex:idx_node_ddns_domain_type" json:"domain"`
	RecordType string `gorm:"column:record_type;type:varchar(8);not null;uniqueIndex:idx_node_ddns_domain_type" json:"type"`
	Role       string `gorm:"type:varchar(10);not null;default:'main'" json:"role"`
	// Source picks the address: AAAA auto|ct|cu|cm|prefix, A panel|iface.
	Source       string `gorm:"type:varchar(16);not null;default:'auto'" json:"source"`
	SourcePrefix string `gorm:"column:source_prefix;type:varchar(64);not null;default:''" json:"prefix"`
	Takeover     bool   `gorm:"not null;default:false" json:"takeover"`
	Inx          int    `gorm:"not null;default:0" json:"inx"`
	// Sync state.
	ZoneID      string `gorm:"column:zone_id;type:varchar(64);not null;default:''" json:"-"`
	RecordID    string `gorm:"column:record_id;type:varchar(64);not null;default:''" json:"-"`
	CurrentIP   string `gorm:"column:current_ip;type:varchar(64);not null;default:''" json:"currentIp"`
	TTL         int    `gorm:"column:ttl;not null;default:0" json:"ttl"`
	Applied     bool   `gorm:"not null;default:false" json:"applied"` // node address field switched to the domain
	Status      string `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	LastError   string `gorm:"column:last_error;type:varchar(255);not null;default:''" json:"lastError"`
	LastSyncAt  int64  `gorm:"column:last_sync_at;type:bigint;not null;default:0" json:"lastSyncAt"`
	LastCheckAt int64  `gorm:"column:last_check_at;type:bigint;not null;default:0" json:"lastCheckAt"`
	FailCount   int    `gorm:"column:fail_count;not null;default:0" json:"failCount"`
	NextRetryAt int64  `gorm:"column:next_retry_at;type:bigint;not null;default:0" json:"nextRetryAt"`
	CreatedTime int64  `gorm:"column:created_time;type:bigint;not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"column:updated_time;type:bigint;not null;default:0" json:"updatedTime"`
}

func (NodeDDNSRecord) TableName() string { return "node_ddns_record" }
