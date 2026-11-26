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
	// Routing (plan 256 「分流」): JSON list of sublink.RouteEntry; NULL (all
	// subscriptions before plan 256) or [] = no 分流 groups, output as before.
	Routing *string `gorm:"type:text" json:"-"`
	// AutoKind (plan 272): "leikwan" = the user's automatic 「利群汇聚」
	// subscription (follows all their 利群 rules; not counted in the 5-sub cap;
	// editable and deletable like any sub, 生成订阅 reuses or re-creates it);
	// "" = an ordinary one.
	AutoKind string `gorm:"type:varchar(20);not null;default:''" json:"autoKind"`
	// EntryMode (plan 297): which entry the subscription's 利群 nodes use —
	// "main" (default, every sub before plan 297) = the owner's 主入口 (follows
	// 换主入口); "local" = 手机本地, 127.0.0.1:<vendor port> (made or updated in
	// the im.gamer App while this phone's entry was 手机本地; works on any
	// device running a LeiKwan client on 127.0.0.1). Nothing else changes.
	EntryMode string `gorm:"type:varchar(10);not null;default:'main'" json:"entryMode"`
	// EntryNames (plan 315 「节点名带入口名」): whether panel rule nodes carry
	// the entry name. NULL (every subscription before plan 315) or true =
	// today's names (「转发名 · 入口名」 on landing rules, chain / external
	// rules with several entries); false = fixed names: the forward name only,
	// 「 · 入口名」 only when this subscription holds the same forward through
	// two or more entries (and a picked entry that no longer exists follows
	// the forward's one remaining entry). The editor and 利群汇聚 「生成订阅」
	// create new subscriptions with false.
	EntryNames *bool `gorm:"column:entry_names" json:"-"`
}

func (UserSubscription) TableName() string { return "user_subscription" }

// FixedNames (plan 315): node names do not follow the entry (entry_names =
// false); NULL / true = the names as before plan 315.
func (s UserSubscription) FixedNames() bool { return s.EntryNames != nil && !*s.EntryNames }

// Sub entry modes (plan 297).
const (
	SubEntryMain  = "main"
	SubEntryLocal = "local"
)

// EntryLocal: the subscription's 利群 nodes are on 手机本地 (127.0.0.1).
func (s UserSubscription) EntryLocal() bool { return s.EntryMode == SubEntryLocal }

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
// Plan 200: 2 external sources per account (was 3); accounts that already
// have more keep them, they just cannot add another.
// Plan 193: 100 nodes per subscription (saved items and rendered output, a
// followed source counts with its nodes) and the first 100 usable nodes of
// an external subscription source.
const (
	MaxSubscriptionsPerUser = 5
	MaxSubSourcesPerUser    = 2
	MaxSubscriptionNodes    = 100
	MaxSubSourceNodes       = 100
)

// UserSubRuleSet (plan 261 「规则集链接」) is a rule list the panel downloaded
// for a user's 分流 entry and serves at /s/rs/u/<PublicID>.<ext>. Only the
// validated domain / IP rules are kept (Rules, normalized classical lines);
// the URL is encrypted like a source URL and never returned. One row per
// user + URL (URLHash = keyed hash of the URL), shared by every subscription
// of that user that references it; deleted when nothing references it.
type UserSubRuleSet struct {
	ID     int64 `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID int64 `gorm:"not null;index;uniqueIndex:idx_user_sub_ruleset_user_url,priority:1" json:"userId"`
	// URLEnc: AES-GCM sealed URL; URLHint: host + masked path for display.
	URLEnc  string `gorm:"column:url_enc;type:text;not null;default:''" json:"-"`
	URLHint string `gorm:"column:url_hint;not null;default:''" json:"urlHint"`
	URLHash string `gorm:"column:url_hash;size:64;not null;default:'';uniqueIndex:idx_user_sub_ruleset_user_url,priority:2" json:"-"`
	// PublicID: 32 hex chars (128 random bits), the public file name.
	PublicID string `gorm:"column:public_id;size:32;not null;uniqueIndex" json:"-"`
	// Rules: one normalized classical line per rule ("DOMAIN-SUFFIX,x").
	Rules        string `gorm:"type:text;not null;default:''" json:"-"`
	RuleCount    int    `gorm:"not null;default:0" json:"ruleCount"`
	CountDomain  int    `gorm:"not null;default:0" json:"countDomain"`
	CountSuffix  int    `gorm:"not null;default:0" json:"countSuffix"`
	CountKeyword int    `gorm:"not null;default:0" json:"countKeyword"`
	CountIP      int    `gorm:"column:count_ip;not null;default:0" json:"countIp"`
	Ignored      int    `gorm:"not null;default:0" json:"ignored"`
	ContentHash  string `gorm:"size:64;not null;default:''" json:"-"`
	// FetchedAt: last successful download; LastTryAt: last attempt (ms).
	FetchedAt   int64  `gorm:"not null;default:0" json:"fetchedAt"`
	LastTryAt   int64  `gorm:"not null;default:0" json:"lastTryAt"`
	LastError   string `gorm:"not null;default:''" json:"lastError"`
	FailCount   int    `gorm:"not null;default:0" json:"failCount"`
	CreatedTime int64  `gorm:"not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null;default:0" json:"updatedTime"`
}

func (UserSubRuleSet) TableName() string { return "user_sub_ruleset" }

// MaxSubRuleSetsPerUser: stored 规则集链接 sets per account (plan 261).
const MaxSubRuleSetsPerUser = 10
