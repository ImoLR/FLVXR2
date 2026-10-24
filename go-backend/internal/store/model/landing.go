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
	RouteProfileID           int64   `gorm:"not null;default:0;index" json:"routeProfileId"`
	EgressHash               string  `gorm:"not null;default:''" json:"-"`
	EgressRestartOK          bool    `gorm:"not null;default:false" json:"egressRestartOk"`
	EgressState              string  `gorm:"not null;default:''" json:"egressState"`
	EgressError              string  `gorm:"type:text;not null;default:''" json:"egressError"`
	EgressReady              bool    `gorm:"not null;default:false" json:"egressReady"`
	EgressCheckedAt          int64   `gorm:"not null;default:0" json:"egressCheckedAt"`
	EgressWarnings           string  `gorm:"type:text;not null;default:''" json:"-"`
	ChainOnly                bool    `gorm:"not null;default:false" json:"chainOnly"` // exit landing auto-created for chain rules (plan 108); never shown to or usable by users
	// Reality guard (plan 127): admin switch (stored inverted, default on) and
	// the last state the agent reported ("", on, restart_required, error).
	RealityGuardOff bool   `gorm:"not null;default:false" json:"-"`
	GuardState      string `gorm:"not null;default:''" json:"-"`
	GuardNodes      int    `gorm:"not null;default:0" json:"-"`
	// Plan 121 (contract landing-protocols-v1, shared servers only): defaults
	// copied into each new AnyTLS / Snell / Mieru node, like the Reality fields.
	AnyTLSServerName string `gorm:"column:anytls_server_name;not null;default:''" json:"anytlsServerName"`
	SnellVersion     int    `gorm:"not null;default:4" json:"snellVersion"`
	SnellObfs        string `gorm:"not null;default:'none'" json:"snellObfs"`
	SnellObfsHost    string `gorm:"not null;default:''" json:"snellObfsHost"`
	MieruTransport   string `gorm:"not null;default:'TCP'" json:"mieruTransport"`
	// Custom's busy_ports (JSON int list), kept out of SharedCaps so the caps
	// blob parsed per node view stays small; read only when picking a port.
	SharedBusyPorts string `gorm:"type:text;not null;default:''" json:"-"`
	// Plan 141: 计算方式 of the per-node connection limit of nodes that follow
	// the server (ConnMode, also the default for new nodes) and of the native
	// server total (MaxConnMode): "total" = 入站+出站 (Core max_total_connections:
	// every TCP socket of the port in any state), "inbound" = 仅入站 (Core
	// max_inbound_connections: accepted client connections, like rule 连接数).
	// A shared server's total belongs to 妙妙屋 and is always "total".
	ConnMode    string `gorm:"not null;default:'total'" json:"connMode"`
	MaxConnMode string `gorm:"not null;default:'total'" json:"maxConnMode"`
	// ConnModeSig is the 仅入站 signature last sent to the native agent; a change
	// advances the revision (the agent rejects a reused revision with new content).
	ConnModeSig string `gorm:"not null;default:''" json:"-"`
	CreatedTime int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null" json:"updatedTime"`
}

func (LandingServer) TableName() string { return "landing_server" }

type LandingServerUserGroup struct {
	LandingServerID int64 `gorm:"primaryKey;autoIncrement:false" json:"landingServerId"`
	UserGroupID     int64 `gorm:"primaryKey;autoIncrement:false" json:"userGroupId"`
}

func (LandingServerUserGroup) TableName() string { return "landing_server_user_group" }

// LandingServerUser grants one user direct access to a landing server, in
// addition to any user-group grant. The primary key serves server-side lookups;
// the user index serves the per-user server list.
type LandingServerUser struct {
	LandingServerID int64 `gorm:"primaryKey;autoIncrement:false" json:"landingServerId"`
	UserID          int64 `gorm:"primaryKey;autoIncrement:false;index:idx_landing_server_user_user" json:"userId"`
	CreatedTime     int64 `gorm:"not null;default:0" json:"createdTime"`
}

func (LandingServerUser) TableName() string { return "landing_server_user" }

type LandingNode struct {
	ID              int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID          int64  `gorm:"not null;index;index:idx_landing_reuse,priority:1" json:"userId"`
	LandingServerID int64  `gorm:"not null;uniqueIndex:idx_landing_server_port;index:idx_landing_reuse,priority:2" json:"landingServerId"`
	Name            string `gorm:"not null" json:"name"`
	Protocol        string `gorm:"not null;index:idx_landing_reuse,priority:3" json:"protocol"`
	Port            int    `gorm:"not null;uniqueIndex:idx_landing_server_port" json:"port"`
	InboundTag      string `gorm:"not null" json:"inboundTag"`
	// GuardPort is the loopback port of this Reality node's guard (plan 127);
	// nodes with the same dest and serverNames on a server share it. 0 = none.
	GuardPort         int      `gorm:"not null;default:0" json:"-"`
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
	Status            string   `gorm:"not null;default:'active';index:idx_landing_reuse,priority:4" json:"status"`
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
	// Plan 141: the Core's live inbound (client) connections of this node, next
	// to Connections (port in+out, all TCP states). Reported by agents with
	// landing_conn_mode >= 1 and by 妙妙屋 shares with conn_modes v1; 0 otherwise.
	InboundConnections int `gorm:"not null;default:0" json:"inboundConnections"`
	// ConnMode overrides the server's 计算方式 for this node; nil follows it.
	ConnMode         *string `json:"connMode"`
	DeletePending    bool    `gorm:"not null;default:false" json:"deletePending"`
	RemoteState      string  `gorm:"not null;default:''" json:"remoteState"`
	SyncError        string  `gorm:"not null;default:''" json:"syncError"`
	RejectedRevision int64   `gorm:"not null;default:0" json:"-"`
	LastCounterAt    int64   `gorm:"not null;default:0" json:"-"`
	RouteProfileID   *int64  `gorm:"index" json:"routeProfileId"`
	// Plan 121 (landing-protocols-v1): AnyTLS password / Snell PSK / Mieru
	// password, Mieru username, the non-secret options fixed at creation
	// (ProtocolOpts JSON: sni, version, obfs, obfsHost, transport) and the
	// AnyTLS self-signed certificate (key secret, fingerprint public).
	Password     string `gorm:"not null;default:''" json:"-"`
	Username     string `gorm:"not null;default:''" json:"-"`
	ProtocolOpts string `gorm:"type:text;not null;default:''" json:"-"`
	TLSCertPEM   string `gorm:"column:tls_cert_pem;type:text;not null;default:''" json:"-"`
	TLSKeyPEM    string `gorm:"column:tls_key_pem;type:text;not null;default:''" json:"-"`
	CertSHA256   string `gorm:"column:cert_sha256;not null;default:''" json:"certSha256"`
	// Plan 134: the owner's node rules (JSON list of landing egress rules,
	// scoped to this node's inbound tag), their number (enabled or not, for
	// the per-node / per-user caps) and the node's own default outbound
	// ("" follows the upper layers). They move with the node and go with it.
	RouteRules     string `gorm:"type:text;not null;default:'[]'" json:"-"`
	RouteRuleCount int    `gorm:"not null;default:0" json:"routeRuleCount"`
	RouteDefault   string `gorm:"not null;default:''" json:"-"`
	CreatedTime    int64  `gorm:"not null;index:idx_landing_reuse,priority:5" json:"createdTime"`
	UpdatedTime    int64  `gorm:"not null" json:"updatedTime"`
}

func (LandingNode) TableName() string { return "landing_node" }

type LandingUserSetting struct {
	UserID   int64 `gorm:"primaryKey;autoIncrement:false" json:"userId"`
	MaxNodes int   `gorm:"not null;default:5" json:"maxNodes"`
	// MaxNodesCustom (plan 131): false = the user follows the global default
	// (vite_config landing_default_max_nodes) and MaxNodes keeps the old hard
	// default 5, so a rollback to an older panel behaves as before.
	MaxNodesCustom bool `gorm:"not null;default:false" json:"maxNodesCustom"`
	// Plan 134 node-rule caps: own values when the matching Custom flag is
	// set, otherwise the global defaults (vite_config
	// landing_node_rule_max_per_node / landing_node_rule_max_per_user).
	RuleMaxNode        int  `gorm:"not null;default:5" json:"ruleMaxNode"`
	RuleMaxNodeCustom  bool `gorm:"not null;default:false" json:"ruleMaxNodeCustom"`
	RuleMaxTotal       int  `gorm:"not null;default:10" json:"ruleMaxTotal"`
	RuleMaxTotalCustom bool `gorm:"not null;default:false" json:"ruleMaxTotalCustom"`
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
