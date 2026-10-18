package model

// Landing models deliberately use portable scalar columns on SQLite and PostgreSQL.
type LandingServer struct {
	ID                       int64   `gorm:"primaryKey;autoIncrement" json:"id"`
	Source                   string  `gorm:"not null;default:'native'" json:"source"`
	NodeID                   int64   `gorm:"not null;default:0;uniqueIndex:idx_landing_native_node,where:node_id > 0" json:"nodeId"`
	RemoteServerID           int64   `gorm:"not null;default:0;uniqueIndex:idx_landing_remote_server,where:source = 'shared'" json:"remoteServerId"`
	Name                     string  `gorm:"not null" json:"name"`
	PublicHost               string  `gorm:"not null" json:"publicHost"`
	PortMin                  int     `gorm:"not null;default:30000" json:"portMin"`
	PortMax                  int     `gorm:"not null;default:39999" json:"portMax"`
	FlowType                 int     `gorm:"not null;default:2" json:"flowType"`
	TrafficRatio             float64 `gorm:"not null;default:1" json:"trafficRatio"`
	MaxConnections           int     `gorm:"not null;default:0" json:"maxConnections"`
	DefaultMaxConnections    int     `gorm:"not null;default:0" json:"defaultMaxConnections"`
	DefaultMaxOnlineIPs      int     `gorm:"not null;default:0" json:"defaultMaxOnlineIps"`
	DefaultTrafficLimitBytes int64   `gorm:"not null;default:0" json:"defaultTrafficLimitBytes"`
	DefaultUserQuotaBytes    int64   `gorm:"not null;default:0" json:"defaultUserQuotaBytes"`
	DefaultExpiryMode        string  `gorm:"not null;default:'follow_user'" json:"defaultExpiryMode"`
	DefaultExpiryDays        int     `gorm:"not null;default:0" json:"defaultExpiryDays"`
	DefaultExpiresAt         *int64  `json:"defaultExpiresAt"`
	MaxNodesPerUser          int     `gorm:"not null;default:3" json:"maxNodesPerUser"`
	Protocols                string  `gorm:"type:text;not null;default:'[\"ss2022\",\"vless_reality_vision\"]'" json:"-"`
	RealityDest              string  `gorm:"not null;default:'www.lovelive-anime.jp:443'" json:"realityDest"`
	RealityServerNames       string  `gorm:"type:text;not null;default:'[\"www.lovelive-anime.jp\"]'" json:"-"`
	AvailableUntil           *int64  `json:"availableUntil"`
	CoreMemoryMB             int     `gorm:"not null;default:256" json:"coreMemoryMb"`
	CoreCPUPercent           int     `gorm:"not null;default:50" json:"coreCpuPercent"`
	Status                   string  `gorm:"not null;default:'active'" json:"status"`
	Revision                 int64   `gorm:"not null;default:1" json:"revision"`
	SharedCaps               string  `gorm:"type:text;not null;default:''" json:"-"`
	SharedLastSyncAt         int64   `gorm:"not null;default:0" json:"sharedLastSyncAt"`
	SharedSyncError          string  `gorm:"not null;default:''" json:"sharedSyncError"`
	SharedDesiredHash        string  `gorm:"not null;default:''" json:"-"`
	SharedPushedRevision     int64   `gorm:"not null;default:0" json:"sharedPushedRevision"`
	SharedAppliedRevision    int64   `gorm:"not null;default:0" json:"sharedAppliedRevision"`
	SharedLastPushAt         int64   `gorm:"not null;default:0" json:"-"`
	SharedCollectedAt        int64   `gorm:"not null;default:0" json:"-"`
	CreatedTime              int64   `gorm:"not null" json:"createdTime"`
	UpdatedTime              int64   `gorm:"not null" json:"updatedTime"`
}

func (LandingServer) TableName() string { return "landing_server" }

type LandingServerUserGroup struct {
	LandingServerID int64 `gorm:"primaryKey;autoIncrement:false" json:"landingServerId"`
	UserGroupID     int64 `gorm:"primaryKey;autoIncrement:false" json:"userGroupId"`
}

func (LandingServerUserGroup) TableName() string { return "landing_server_user_group" }

type LandingNode struct {
	ID                int64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID            int64    `gorm:"not null;index" json:"userId"`
	LandingServerID   int64    `gorm:"not null;uniqueIndex:idx_landing_server_port" json:"landingServerId"`
	Name              string   `gorm:"not null" json:"name"`
	Protocol          string   `gorm:"not null" json:"protocol"`
	Port              int      `gorm:"not null;uniqueIndex:idx_landing_server_port" json:"port"`
	InboundTag        string   `gorm:"not null" json:"inboundTag"`
	Method            string   `gorm:"not null;default:''" json:"method"`
	ServerKey         string   `gorm:"not null;default:''" json:"-"`
	UserKey           string   `gorm:"not null;default:''" json:"-"`
	UUID              string   `gorm:"not null;default:''" json:"-"`
	Flow              string   `gorm:"not null;default:''" json:"flow"`
	PrivateKey        string   `gorm:"not null;default:''" json:"-"`
	PublicKey         string   `gorm:"not null;default:''" json:"publicKey"`
	ShortID           string   `gorm:"not null;default:''" json:"shortId"`
	Dest              string   `gorm:"not null;default:''" json:"dest"`
	ServerNames       string   `gorm:"type:text;not null;default:'[]'" json:"-"`
	MaxConnections    int      `gorm:"not null;default:0" json:"maxConnections"`
	MaxOnlineIPs      int      `gorm:"not null;default:0" json:"maxOnlineIps"`
	TrafficLimitBytes int64    `gorm:"not null;default:0" json:"trafficLimitBytes"`
	FlowType          *int     `json:"flowType"`
	TrafficRatio      *float64 `json:"trafficRatio"`
	ExpiryMode        string   `gorm:"not null;default:'follow_user'" json:"expiryMode"`
	ExpiresAt         *int64   `json:"expiresAt"`
	Status            string   `gorm:"not null;default:'active'" json:"status"`
	AutoDisabled      bool     `gorm:"not null;default:false" json:"autoDisabled"`
	ExpiredAt         *int64   `json:"expiredAt"`
	UpBytes           int64    `gorm:"not null;default:0" json:"upBytes"`
	DownBytes         int64    `gorm:"not null;default:0" json:"downBytes"`
	BilledIn          int64    `gorm:"not null;default:0" json:"billedIn"`
	BilledOut         int64    `gorm:"not null;default:0" json:"billedOut"`
	PeriodKey         int64    `gorm:"not null;default:0" json:"periodKey"`
	CounterEpoch      string   `gorm:"not null;default:''" json:"-"`
	LastSeenUp        int64    `gorm:"not null;default:0" json:"-"`
	LastSeenDown      int64    `gorm:"not null;default:0" json:"-"`
	Connections       int      `gorm:"not null;default:0" json:"connections"`
	DeletePending     bool     `gorm:"not null;default:false" json:"-"`
	RemoteState       string   `gorm:"not null;default:''" json:"remoteState"`
	SyncError         string   `gorm:"not null;default:''" json:"syncError"`
	RejectedRevision  int64    `gorm:"not null;default:0" json:"-"`
	LastCounterAt     int64    `gorm:"not null;default:0" json:"-"`
	CreatedTime       int64    `gorm:"not null" json:"createdTime"`
	UpdatedTime       int64    `gorm:"not null" json:"updatedTime"`
}

func (LandingNode) TableName() string { return "landing_node" }

type LandingUserSetting struct {
	UserID   int64 `gorm:"primaryKey;autoIncrement:false" json:"userId"`
	MaxNodes int   `gorm:"not null;default:5" json:"maxNodes"`
}

func (LandingUserSetting) TableName() string { return "landing_user_setting" }

type UserServerQuota struct {
	ID            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        int64  `gorm:"not null;uniqueIndex:idx_user_server_quota" json:"userId"`
	ServerKind    string `gorm:"not null;uniqueIndex:idx_user_server_quota" json:"serverKind"`
	ServerRefID   int64  `gorm:"not null;uniqueIndex:idx_user_server_quota" json:"serverRefId"`
	LimitBytes    int64  `gorm:"not null;default:0" json:"limitBytes"`
	LimitOverride bool   `gorm:"not null;default:false" json:"limitOverride"`
	UsedIn        int64  `gorm:"not null;default:0" json:"usedIn"`
	UsedOut       int64  `gorm:"not null;default:0" json:"usedOut"`
	PeriodKey     int64  `gorm:"not null;default:0" json:"periodKey"`
	Paused        bool   `gorm:"not null;default:false" json:"paused"`
	CreatedTime   int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime   int64  `gorm:"not null" json:"updatedTime"`
}

func (UserServerQuota) TableName() string { return "user_server_quota" }

// Stored with the same private-column convention as LeiKwanAccount secrets.
type LandingIntegration struct {
	ID          int64  `gorm:"primaryKey;autoIncrement:false" json:"-"`
	BaseURL     string `gorm:"not null" json:"baseUrl"`
	Token       string `gorm:"type:text;not null" json:"-"`
	LastSyncAt  int64  `gorm:"not null;default:0" json:"lastSyncAt"`
	SyncError   string `gorm:"not null;default:''" json:"syncError"`
	UpdatedTime int64  `gorm:"not null" json:"updatedTime"`
}

func (LandingIntegration) TableName() string { return "landing_integration" }
