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
	// Plan 129 (native): CoreStaleEpoch is the epoch of a running Core the
	// agent reports as older than the pinned binary on disk ("" = current or
	// unknown); CoreUpgradeOK is the admin's one-shot confirmation to restart
	// exactly that Core, cleared once the agent reports a current Core.
	CoreStaleEpoch string `gorm:"not null;default:''" json:"-"`
	CoreUpgradeOK  bool   `gorm:"not null;default:false" json:"-"`
	// Plan 147 (native): RuleCarved records, in rule-spec syntax, the ports
	// this landing actually took out of its node's rule port range ("" = none
	// recorded, e.g. carved before plan 147). Give-back returns them even when
	// they sat at an outer edge of the rule range; nothing else is invented.
	RuleCarved string `gorm:"type:text;not null;default:''" json:"-"`
	// Plan 147 (native): ports the agent reported busy on the machine (another
	// program listens there), JSON {"<port>": unix ms}; skipped by the port
	// picker for 24 h after the last report.
	NativeBusyPorts string `gorm:"type:text;not null;default:''" json:"-"`
	// Plan 161 (native): why the agent could not apply the per-user 速率 in
	// the kernel ("" = applied, nothing to apply, or an agent without it).
	RateError   string `gorm:"type:text;not null;default:''" json:"-"`
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
	ConnMode      *string `json:"connMode"`
	DeletePending bool    `gorm:"not null;default:false" json:"deletePending"`
	RemoteState   string  `gorm:"not null;default:''" json:"remoteState"`
	SyncError     string  `gorm:"not null;default:''" json:"syncError"`
	// Plan 147 (native): automatic port moves after the agent reported the
	// node's port busy on the machine (never-used nodes only, at most 3).
	PortMoves        int    `gorm:"not null;default:0" json:"-"`
	RejectedRevision int64  `gorm:"not null;default:0" json:"-"`
	LastCounterAt    int64  `gorm:"not null;default:0" json:"-"`
	RouteProfileID   *int64 `gorm:"index" json:"routeProfileId"`
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
	// Plan 177 「链式代理」: the owner's external node all traffic of this node
	// leaves through (0 = 直连 / the normal outbound layers). Block rules still
	// apply first; no fallback to direct when the external node fails.
	ChainExternalID int64 `gorm:"not null;default:0;index" json:"chainExternalId"`
	CreatedTime     int64 `gorm:"not null;index:idx_landing_reuse,priority:5" json:"createdTime"`
	UpdatedTime     int64 `gorm:"not null" json:"updatedTime"`
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
	// MaxInboundConnections (plan 158) = 入站连接数 of this user on this server
	// (0 = 不限): an entry server counts the user's forward inbound connections
	// entering there, a landing server the user's inbound connections on its
	// Core (relayed ones included — the Core cannot tell them apart).
	// Enforced through the quota coordinator; older panels ignore the column.
	MaxInboundConnections int `gorm:"not null;default:0" json:"maxInboundConnections"`
	// RateMbps / RateOverride (plan 159) = 速率 of this user on this server:
	// one shared limit for all of the user's rules entering the machine.
	// RateOverride false follows the user's default (user.server_rate_mbps);
	// true uses RateMbps, where 0 = 不限. Older panels ignore both columns.
	RateMbps     int   `gorm:"not null;default:0" json:"rateMbps"`
	RateOverride bool  `gorm:"not null;default:false" json:"rateOverride"`
	CreatedTime  int64 `gorm:"not null" json:"createdTime"`
	UpdatedTime  int64 `gorm:"not null" json:"updatedTime"`
	// FollowDefault (request only, plan 158): the row keeps following the
	// server's default traffic quota; a save changes only its 入站连接数.
	FollowDefault bool `gorm:"-" json:"followDefault,omitempty"`
	// RateKeep (request only, plan 159): the client sent no 速率 fields (an
	// older frontend); the save keeps the stored rate instead of clearing it.
	RateKeep bool `gorm:"-" json:"-"`
}

func (UserServerQuota) TableName() string { return "user_server_quota" }

// UserTrafficPool is a per-user 共享流量池 (plan 157): several servers of the
// user share one billed-bytes limit. A flow crossing several members counts
// once, so Used is not the sum of the members' per-server usage. The period
// follows the user's reset day like UserServerQuota.
type UserTrafficPool struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID      int64  `gorm:"not null;index:idx_user_traffic_pool_user" json:"userId"`
	Name        string `gorm:"not null;default:''" json:"name"`
	LimitBytes  int64  `gorm:"not null;default:0" json:"limitBytes"`
	UsedIn      int64  `gorm:"not null;default:0" json:"usedIn"`
	UsedOut     int64  `gorm:"not null;default:0" json:"usedOut"`
	PeriodKey   int64  `gorm:"not null;default:0" json:"periodKey"`
	Paused      bool   `gorm:"not null;default:false" json:"paused"`
	CreatedTime int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null" json:"updatedTime"`
}

func (UserTrafficPool) TableName() string { return "user_traffic_pool" }

// UserTrafficPoolMember puts one server (same kind/ref as UserServerQuota:
// node = FLVX node incl. native landing, landing = shared server) into a pool.
// The unique index keeps a server in at most one pool per user.
type UserTrafficPoolMember struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"-"`
	PoolID      int64  `gorm:"not null;index:idx_user_traffic_pool_member_pool" json:"-"`
	UserID      int64  `gorm:"not null;uniqueIndex:idx_user_traffic_pool_member" json:"-"`
	ServerKind  string `gorm:"not null;uniqueIndex:idx_user_traffic_pool_member" json:"serverKind"`
	ServerRefID int64  `gorm:"not null;uniqueIndex:idx_user_traffic_pool_member" json:"serverRefId"`
}

func (UserTrafficPoolMember) TableName() string { return "user_traffic_pool_member" }

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
