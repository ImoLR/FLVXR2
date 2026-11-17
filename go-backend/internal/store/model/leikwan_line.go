package model

// Plan 266 利群专线入面板 (step 1). These tables are deliberately separate from
// tunnel / forward / forward_port: a 利群 rule is served by the LeiKwan client
// on the entry machine, so nothing here may ever make FLVX deploy a service on
// the LeiKwan listen port, and no existing runtime path reads these tables.

// LeiKwanLine is one LeiKwan route offered as a tunnel of the type 「利群专线」.
// It has no entry nodes: a user's entry comes from their LeiKwanGrant.
type LeiKwanLine struct {
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountID  int64  `gorm:"not null;uniqueIndex:idx_leikwan_line_route" json:"accountId"`
	RouteID    int64  `gorm:"not null;uniqueIndex:idx_leikwan_line_route" json:"routeId"`
	Name       string `gorm:"type:varchar(100);not null" json:"name"`
	RouteLabel string `gorm:"type:varchar(100);not null;default:''" json:"routeLabel"`
	Transport  string `gorm:"type:varchar(10);not null;default:'tcp+udp'" json:"transport"`
	// BackupTunnelID: the FLVX port-forward tunnel the automatic 备线 of this
	// line's rules is created on (0 = no automatic backup).
	BackupTunnelID int64 `gorm:"not null;default:0" json:"backupTunnelId"`
	// Accel holds the 利群加速 defaults (JSON of LeiKwanAccel).
	Accel       string `gorm:"type:text;not null;default:''" json:"-"`
	Inx         int    `gorm:"not null;default:0" json:"inx"`
	Status      int    `gorm:"not null;default:1" json:"status"`
	CreatedTime int64  `gorm:"not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null;default:0" json:"updatedTime"`
	// Plan 269: billing of the 利群 bytes like a tunnel (1 = 单向, 2 = 双向; ratio).
	FlowMode     int     `gorm:"not null;default:1" json:"flowMode"`
	TrafficRatio float64 `gorm:"not null;default:1" json:"trafficRatio"`
}

func (LeiKwanLine) TableName() string { return "leikwan_line" }

// LeiKwanGrant: the tunnel group 「利群汇聚」 granted to one user. The main entry
// (a node of the 「利群入口」 node group) is picked here, once for every 利群
// line; the user's rule addresses follow it.
type LeiKwanGrant struct {
	ID              int64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID          int64 `gorm:"not null;uniqueIndex" json:"userId"`
	MainEntryNodeID int64 `gorm:"not null;default:0" json:"mainEntryNodeId"`
	// BackupEntryNodeID is reserved for the parked 备用入口 idea (no UI yet).
	BackupEntryNodeID int64 `gorm:"not null;default:0" json:"backupEntryNodeId"`
	// BackupLine 1 = every 利群 rule of the user gets an automatic 备线.
	BackupLine  int   `gorm:"not null;default:1" json:"backupLine"`
	Num         int   `gorm:"not null;default:0" json:"num"`
	FlowGB      int64 `gorm:"column:flow_gb;not null;default:0" json:"flowGb"`
	ExpTime     int64 `gorm:"not null;default:0" json:"expTime"`
	Status      int   `gorm:"not null;default:1" json:"status"`
	CreatedTime int64 `gorm:"not null;default:0" json:"createdTime"`
	UpdatedTime int64 `gorm:"not null;default:0" json:"updatedTime"`
	// Plan 272 换主入口宽限: the 主入口 before the last change stays open (when
	// the global 「只开主入口」 is on) until the user pulled a subscription
	// after EntryChangedAt, or 24 h; 0 = no grace running.
	PrevEntryNodeID int64 `gorm:"not null;default:0" json:"prevEntryNodeId"`
	EntryChangedAt  int64 `gorm:"not null;default:0" json:"entryChangedAt"`
}

func (LeiKwanGrant) TableName() string { return "leikwan_grant" }

// LeiKwanRule is one LeiKwan forward registered as a rule on a 利群专线.
type LeiKwanRule struct {
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     int64  `gorm:"not null;index" json:"userId"`
	LineID     int64  `gorm:"not null;index" json:"lineId"`
	AccountID  int64  `gorm:"not null;default:0" json:"accountId"`
	ForwardID  int64  `gorm:"not null;uniqueIndex" json:"forwardId"`
	Name       string `gorm:"type:varchar(100);not null" json:"name"`
	ListenPort int    `gorm:"not null;default:0" json:"listenPort"`
	Transport  string `gorm:"type:varchar(10);not null;default:''" json:"transport"`
	// LandingNodeID > 0: the target is the user's panel landing node
	// (landing_node.id); 0 = manual host:port (admin only).
	LandingNodeID int64  `gorm:"not null;default:0" json:"landingNodeId"`
	TargetHost    string `gorm:"type:varchar(255);not null;default:''" json:"targetHost"`
	TargetPort    int    `gorm:"not null;default:0" json:"targetPort"`
	ETag          string `gorm:"type:varchar(100);not null;default:''" json:"-"`
	// BackupForwardID: the FLVX forward that is this rule's 备线 (0 = none).
	// BackupMode: auto = created by the panel (deleted with the rule),
	// linked = an existing forward attached by the import.
	BackupForwardID int64  `gorm:"not null;default:0;index" json:"backupForwardId"`
	BackupMode      string `gorm:"type:varchar(10);not null;default:''" json:"backupMode"`
	BackupError     string `gorm:"type:text;not null;default:''" json:"backupError"`
	// Accel: per-rule 利群加速 overrides (JSON of LeiKwanRuleAccel).
	Accel       string `gorm:"type:text;not null;default:''" json:"-"`
	Source      string `gorm:"type:varchar(10);not null;default:'panel'" json:"source"`
	Status      int    `gorm:"not null;default:1" json:"status"`
	CreatedTime int64  `gorm:"not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null;default:0" json:"updatedTime"`
	// Plan 269: billed bytes since the owner's last reset (entry-host nft
	// counters × the line's mode/ratio), the current block reason ('' = open)
	// and a prepared panel landing node waiting for the user's 换落地.
	InFlow               int64  `gorm:"not null;default:0" json:"inFlow"`
	OutFlow              int64  `gorm:"not null;default:0" json:"outFlow"`
	BlockReason          string `gorm:"type:varchar(100);not null;default:''" json:"blockReason"`
	PendingLandingNodeID int64  `gorm:"not null;default:0" json:"pendingLandingNodeId"`
	// Plan 271 备线流量: billed bytes of the rule's 备线 forward since the
	// owner's last reset (a shared 备线 adds its full bytes to every rule
	// using it). Added by the flow batch only; SaveLeiKwanRule never writes
	// the counters.
	BackupInFlow  int64 `gorm:"not null;default:0" json:"backupInFlow"`
	BackupOutFlow int64 `gorm:"not null;default:0" json:"backupOutFlow"`
	// Plan 272: ExternalNodeID > 0 = the target is the owner's 外部节点
	// (landing_external_node.id, TargetHost/Port = its server:port);
	// AllEntries 1 = 「所有利群入口都可用」 (admin only; not blocked on the
	// owner's other 利群入口 when 「只开主入口」 is on). Written by
	// UpdateColumn only (SaveLeiKwanRule omits it).
	ExternalNodeID int64 `gorm:"not null;default:0" json:"externalNodeId"`
	AllEntries     int   `gorm:"not null;default:0" json:"allEntries"`
}

func (LeiKwanRule) TableName() string { return "leikwan_rule" }

// LeiKwanSetting is the single settings row (ID 1) of the 利群汇聚 group.
type LeiKwanSetting struct {
	ID           int64  `gorm:"primaryKey" json:"id"`
	AccountID    int64  `gorm:"not null;default:0" json:"accountId"`
	EntryGroupID int64  `gorm:"not null;default:0" json:"entryGroupId"`
	GroupName    string `gorm:"type:varchar(100);not null;default:'利群汇聚'" json:"groupName"`
	// Global 利群加速 parts (JSON of LeiKwanGlobalAccel): 日志快切 timings and
	// the default 首包合并 mode with per-entry-host overrides.
	Global      string `gorm:"type:text;not null;default:''" json:"-"`
	UpdatedTime int64  `gorm:"not null;default:0" json:"updatedTime"`
}

func (LeiKwanSetting) TableName() string { return "leikwan_setting" }

// LeiKwanHostState is the latest read-only status of a 利群入口 machine
// (step 1: an imported, secret-free SSH snapshot; step 2: agent reports).
type LeiKwanHostState struct {
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	NodeID     int64  `gorm:"not null;uniqueIndex" json:"nodeId"`
	Data       string `gorm:"type:text;not null;default:''" json:"-"`
	Source     string `gorm:"type:varchar(20);not null;default:''" json:"source"`
	CapturedAt int64  `gorm:"not null;default:0" json:"capturedAt"`
}

func (LeiKwanHostState) TableName() string { return "leikwan_host_state" }

// LeiKwanAccel: the 利群加速 defaults of a line.
type LeiKwanAccel struct {
	Warm        LeiKwanWarm `json:"warm"`
	SSFilter    string      `json:"ssFilter"` // enforce | off
	Logswitch   bool        `json:"logswitch"`
	ReturnCheck bool        `json:"returnCheck"`
}

// LeiKwanWarm mirrors the entry front's warm file of one line.
type LeiKwanWarm struct {
	Main    int  `json:"main"`    // main-line pool size (0 = off)
	Backup  int  `json:"backup"`  // backup-line pool size
	Standby int  `json:"standby"` // backup_standby
	PaceMs  int  `json:"paceMs"`  // paced refill gap
	Flush   bool `json:"flush"`   // flush on switch and on return
	// Burst headroom is under A/B test on 百度云广州; '' = not set.
	Burst string `json:"burst"`
}

// LeiKwanRuleAccel: per-rule overrides; nil = the line default.
type LeiKwanRuleAccel struct {
	WarmMain    *int    `json:"warmMain,omitempty"`
	SSFilter    *string `json:"ssFilter,omitempty"`
	Logswitch   *bool   `json:"logswitch,omitempty"`
	ReturnCheck *bool   `json:"returnCheck,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// LeiKwanGlobalAccel: the global 利群加速 parts.
type LeiKwanGlobalAccel struct {
	HoldS          int               `json:"holdS"`
	LeaseSettleS   int               `json:"leaseSettleS"`
	Grace          string            `json:"grace"`
	ReturnCheckS   float64           `json:"returnCheckS"`
	IOError        string            `json:"ioError"`
	Coalesce       string            `json:"coalesce"`
	CoalesceByNode map[string]string `json:"coalesceByNode"`
	// Plan 269: restarts / nft changes on the entry hosts only in the night
	// window (default off).
	NightOnly bool `json:"nightOnly,omitempty"`
	// Plan 272 「只开主入口」: a rule's port is blocked on the 利群入口 hosts
	// that are not its owner's 主入口 (old entry: grace until a sub pull / 24 h;
	// rules with AllEntries stay open everywhere). Off by default.
	EntryLock bool `json:"entryLock,omitempty"`
}

// LeiKwanUsageSnapshot (plan 273) is the last read of one vendor report of
// one 利群 account: kind usage (current cycle), previous (last closed cycle),
// devices or service (services list row: plan, expires_at). Data = trimmed
// JSON of the leikwan.* type ("" until the first good read); FetchedAt = the
// last good read, CheckedAt / Error / ErrorCount = the last attempt, NextAt =
// the next planned attempt (kept across restarts so a back-off survives).
type LeiKwanUsageSnapshot struct {
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountID  int64  `gorm:"not null;uniqueIndex:idx_leikwan_usage_snapshot" json:"accountId"`
	Kind       string `gorm:"type:varchar(20);not null;uniqueIndex:idx_leikwan_usage_snapshot" json:"kind"`
	ServiceID  int64  `gorm:"not null;default:0" json:"serviceId"`
	Data       string `gorm:"type:text;not null;default:''" json:"-"`
	FetchedAt  int64  `gorm:"not null;default:0" json:"fetchedAt"`
	CheckedAt  int64  `gorm:"not null;default:0" json:"checkedAt"`
	Error      string `gorm:"type:varchar(200);not null;default:''" json:"error"`
	ErrorCount int    `gorm:"not null;default:0" json:"errorCount"`
	NextAt     int64  `gorm:"not null;default:0" json:"nextAt"`
}

func (LeiKwanUsageSnapshot) TableName() string { return "leikwan_usage_snapshot" }
