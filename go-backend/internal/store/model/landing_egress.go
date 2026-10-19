package model

// Landing outbound/routing models (plan 107). Portable scalar columns only;
// JSON payloads are stored as text.

type LandingOutbound struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"not null" json:"name"`
	Type        string `gorm:"not null" json:"type"`
	IPVersion   string `gorm:"not null;default:'system'" json:"ipVersion"`
	Settings    string `gorm:"type:text;not null;default:'{}'" json:"-"`
	Remark      string `gorm:"not null;default:''" json:"remark"`
	CreatedTime int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null" json:"updatedTime"`
}

func (LandingOutbound) TableName() string { return "landing_outbound" }

type LandingRouteProfile struct {
	ID            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name          string `gorm:"not null" json:"name"`
	DefaultAction string `gorm:"not null;default:'direct'" json:"defaultAction"`
	Rules         string `gorm:"type:text;not null;default:'[]'" json:"-"`
	CreatedTime   int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime   int64  `gorm:"not null" json:"updatedTime"`
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
}

func (LandingEgressSetting) TableName() string { return "landing_egress_setting" }
