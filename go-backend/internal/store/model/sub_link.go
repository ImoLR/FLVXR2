package model

// Plan 185 「订阅」: a user's own subscription URL (/s/<token>) that merges the
// user's panel nodes (rule links, landing 直连, 「我自己的节点」 chain links),
// external nodes and nodes of external subscriptions. A subscription stores
// references only; nodes are rendered live on every pull.

// UserSubscription is one subscription link of a user.
type UserSubscription struct {
	ID     int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID int64  `gorm:"not null;index" json:"userId"`
	Name   string `gorm:"not null;default:''" json:"name"`
	// Token is the only secret of the link (22+ random base62 chars).
	Token string `gorm:"not null;uniqueIndex" json:"-"`
	// Format: auto (by User-Agent) | clash | singbox | base64.
	Format string `gorm:"not null;default:'auto'" json:"format"`
	// RuleMode: cn_direct (国内直连) | global (全部走代理).
	RuleMode string `gorm:"not null;default:'cn_direct'" json:"ruleMode"`
	// Items: ordered JSON list of references (sublink.Ref).
	Items          string `gorm:"type:text;not null;default:'[]'" json:"-"`
	LastPullAt     int64  `gorm:"not null;default:0" json:"lastPullAt"`
	LastPullClient string `gorm:"not null;default:''" json:"lastPullClient"`
	LastPullNodes  int    `gorm:"not null;default:0" json:"lastPullNodes"`
	CreatedTime    int64  `gorm:"not null;default:0" json:"createdTime"`
	UpdatedTime    int64  `gorm:"not null;default:0" json:"updatedTime"`
}

func (UserSubscription) TableName() string { return "user_subscription" }

// UserSubSource is an external subscription (机场) whose nodes can be put into
// subscriptions. The URL is encrypted at rest and never returned in full;
// the nodes of the last good fetch are cached and refreshed lazily.
type UserSubSource struct {
	ID     int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID int64  `gorm:"not null;index" json:"userId"`
	Name   string `gorm:"not null;default:''" json:"name"`
	// URLEnc: AES-GCM sealed URL; URLHint: host + masked path for display.
	URLEnc  string `gorm:"column:url_enc;type:text;not null;default:''" json:"-"`
	URLHint string `gorm:"column:url_hint;not null;default:''" json:"urlHint"`
	// LastOkAt: last successful fetch; LastFetchAt: last attempt (ms).
	LastOkAt    int64  `gorm:"not null;default:0" json:"lastOkAt"`
	LastFetchAt int64  `gorm:"not null;default:0" json:"lastFetchAt"`
	LastError   string `gorm:"not null;default:''" json:"lastError"`
	// CachedNodes: JSON list of the parsed nodes (holds credentials, never
	// returned by any API).
	CachedNodes string `gorm:"type:text;not null;default:'[]'" json:"-"`
	NodeCount   int    `gorm:"not null;default:0" json:"nodeCount"`
	CreatedTime int64  `gorm:"not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null;default:0" json:"updatedTime"`
}

func (UserSubSource) TableName() string { return "user_sub_source" }

// Per-user caps (admins: unlimited subscriptions, same source cap).
// Plan 193: 100 nodes per subscription (saved items and rendered output, a
// followed source counts with its nodes) and the first 100 usable nodes of
// an external subscription source.
const (
	MaxSubscriptionsPerUser = 5
	MaxSubSourcesPerUser    = 3
	MaxSubscriptionNodes    = 100
	MaxSubSourceNodes       = 100
)
