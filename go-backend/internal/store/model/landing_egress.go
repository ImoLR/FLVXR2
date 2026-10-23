package model

// Landing outbound/routing models (plan 107). Portable scalar columns only;
// JSON payloads are stored as text.

type LandingOutbound struct {
	ID        int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string `gorm:"not null" json:"name"`
	Type      string `gorm:"not null" json:"type"`
	IPVersion string `gorm:"not null;default:'system'" json:"ipVersion"`
	Settings  string `gorm:"type:text;not null;default:'{}'" json:"-"`
	Remark    string `gorm:"not null;default:''" json:"remark"`
	// UserAllowed (plan 134): users may pick this template in their own node
	// rules and as their node's default outbound. Off by default.
	UserAllowed bool  `gorm:"not null;default:false" json:"userAllowed"`
	CreatedTime int64 `gorm:"not null" json:"createdTime"`
	UpdatedTime int64 `gorm:"not null" json:"updatedTime"`
}

func (LandingOutbound) TableName() string { return "landing_outbound" }

type LandingRouteProfile struct {
	ID            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name          string `gorm:"not null" json:"name"`
	DefaultAction string `gorm:"not null;default:'direct'" json:"defaultAction"`
	Rules         string `gorm:"type:text;not null;default:'[]'" json:"-"`
	// AbuseDefaultsOff switches off the built-in anti-abuse rules (plan 127,
	// TCP 25 + BitTorrent) for nodes on this profile. Stored inverted so the
	// added column defaults to on for existing profiles.
	AbuseDefaultsOff bool  `gorm:"not null;default:false" json:"-"`
	CreatedTime      int64 `gorm:"not null" json:"createdTime"`
	UpdatedTime      int64 `gorm:"not null" json:"updatedTime"`
}

func (LandingRouteProfile) TableName() string { return "landing_route_profile" }

// LandingWarpAccount is the auto-registered WARP device of one landing server
// for one WARP template. Token and private key are secrets.
type LandingWarpAccount struct {
	ID              int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	LandingServerID int64  `gorm:"not null;uniqueIndex:idx_landing_warp_account" json:"landingServerId"`
	OutboundID      int64  `gorm:"not null;uniqueIndex:idx_landing_warp_account;index" json:"outboundId"`
	DeviceID        string `gorm:"not null;default:''" json:"-"`
	Token           string `gorm:"type:text;not null;default:''" json:"-"`
	PrivateKey      string `gorm:"not null;default:''" json:"-"`
	PublicKey       string `gorm:"not null;default:''" json:"publicKey"`
	PeerPublicKey   string `gorm:"not null;default:''" json:"-"`
	AddressV4       string `gorm:"not null;default:''" json:"addressV4"`
	AddressV6       string `gorm:"not null;default:''" json:"addressV6"`
	EndpointV4      string `gorm:"not null;default:''" json:"-"`
	EndpointV6      string `gorm:"not null;default:''" json:"-"`
	Reserved        string `gorm:"not null;default:''" json:"-"`
	AccountType     string `gorm:"not null;default:''" json:"accountType"`
	LicenseHash     string `gorm:"not null;default:''" json:"-"`
	Status          string `gorm:"not null;default:'pending'" json:"status"`
	LastError       string `gorm:"not null;default:''" json:"lastError"`
	Failures        int    `gorm:"not null;default:0" json:"-"`
	NextAttemptAt   int64  `gorm:"not null;default:0" json:"-"`
	CreatedTime     int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime     int64  `gorm:"not null" json:"updatedTime"`
}

func (LandingWarpAccount) TableName() string { return "landing_warp_account" }

// LandingEgressSetting is a single row (id 1) of global egress settings.
type LandingEgressSetting struct {
	ID              int64  `gorm:"primaryKey;autoIncrement:false" json:"-"`
	SourceWhitelist string `gorm:"type:text;not null;default:'[]'" json:"-"`
	UpdatedTime     int64  `gorm:"not null;default:0" json:"updatedTime"`
	// Global rule group (plan 134), applied to every landing server: 全局·强制
	// rules (before node rules), 全局·默认 rules (after the server profile) and
	// the 全局默认出站. Empty lists + direct = no change to any plan.
	GlobalForced      string `gorm:"type:text;not null;default:'[]'" json:"-"`
	GlobalDefault     string `gorm:"type:text;not null;default:'[]'" json:"-"`
	GlobalAction      string `gorm:"not null;default:'direct'" json:"-"`
	GlobalUpdatedTime int64  `gorm:"not null;default:0" json:"-"`
	// Plan 140: global switches of the built-in anti-abuse rules (NULL = on,
	// i.e. the pre-140 behaviour); a node blocks only when the switch AND its
	// routing profile's 防滥用默认 toggle are on.
	BuiltinSMTP *bool `gorm:"column:builtin_smtp" json:"-"`
	BuiltinBT   *bool `gorm:"column:builtin_bt" json:"-"`
	// Plan 143: the 防滥用 rows moved here from the 全局·强制 seeds (mail
	// submission ports 465/587/2525, Telnet/SMB). NULL = OFF (unlike SMTP /
	// BT): they were opt-in seeds, so a fresh install or an old row never
	// starts blocking them silently; the one-time migration writes the state
	// the seed had.
	BuiltinMailPorts *bool `gorm:"column:builtin_mail_ports" json:"-"`
	BuiltinTelnetSMB *bool `gorm:"column:builtin_telnet_smb" json:"-"`
	// One-time seed markers (plan 140): the disabled 全局·强制 starter rules and
	// the preset library. Once set, deleted seeds never come back.
	// GlobalSeedVersion 2 (plan 143): the 防滥用 seeds were moved to 内置.
	GlobalSeedVersion int `gorm:"not null;default:0" json:"-"`
	PresetSeedVersion int `gorm:"not null;default:0" json:"-"`
}

func (LandingEgressSetting) TableName() string { return "landing_egress_setting" }

// LandingEgressPreset is one entry of the routing-rule preset library (plan
// 140). Rules is the landingegress.Rule list as JSON, without ids; rules of an
// AskOutbound preset carry the action "ask", replaced at insert time by the
// outbound the admin / user picks. SeedKey names a built-in seed ("" = made
// by an admin); 「恢复默认预设」 re-adds missing seed keys.
type LandingEgressPreset struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"not null" json:"name"`
	Description string `gorm:"type:text;not null;default:''" json:"description"`
	Rules       string `gorm:"type:text;not null;default:'[]'" json:"-"`
	AskOutbound bool   `gorm:"not null;default:false" json:"askOutbound"`
	InsertTop   bool   `gorm:"not null;default:false" json:"insertTop"`
	SortOrder   int    `gorm:"not null;default:0;index" json:"sortOrder"`
	SeedKey     string `gorm:"not null;default:''" json:"seedKey"`
	CreatedTime int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null" json:"updatedTime"`
}

func (LandingEgressPreset) TableName() string { return "landing_egress_preset" }
