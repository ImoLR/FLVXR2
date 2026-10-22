package model

// Line-machine chain rules (plan 108). A ForwardChain is the user's own node
// behind a 线路机 tunnel: one hidden VLESS Reality Vision node per tunnel exit
// (ForwardChainNode), listening on 127.0.0.1 and leaving through the user's
// node. Portable scalar columns only; the upstream JSON is stored as text.

// Chain states.
const (
	ChainStateCreating = "creating"
	ChainStateActive   = "active"
	ChainStateFailed   = "failed"
)

type ForwardChain struct {
	ID        int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    int64  `gorm:"not null;index;uniqueIndex:idx_forward_chain_idem,where:idem_key <> ''" json:"userId"`
	ForwardID int64  `gorm:"not null;default:0;index" json:"forwardId"`
	TunnelID  int64  `gorm:"not null;default:0;index" json:"tunnelId"`
	State     string `gorm:"not null;default:'creating';index" json:"state"`
	Progress  string `gorm:"not null;default:''" json:"progress"`
	LastError string `gorm:"type:text;not null;default:''" json:"lastError"`
	// Port is the loopback port, identical on every exit copy.
	Port int `gorm:"not null;default:0" json:"-"`
	// Reality inbound parameters shared by all exit copies (the user's link).
	UUID        string `gorm:"not null;default:''" json:"-"`
	PrivateKey  string `gorm:"not null;default:''" json:"-"`
	PublicKey   string `gorm:"not null;default:''" json:"-"`
	ShortID     string `gorm:"not null;default:''" json:"-"`
	Dest        string `gorm:"not null;default:''" json:"-"`
	ServerNames string `gorm:"type:text;not null;default:'[]'" json:"-"`
	// Upstream is the typed outbound of the user's node (secret, never sent
	// to clients); PrevUpstream is the rollback point while a link change
	// is being applied.
	Upstream         string `gorm:"type:text;not null;default:''" json:"-"`
	PrevUpstream     string `gorm:"type:text;not null;default:''" json:"-"`
	UpstreamProtocol string `gorm:"not null;default:''" json:"upstreamProtocol"`
	UpstreamHost     string `gorm:"not null;default:''" json:"upstreamHost"`
	UpstreamPort     int    `gorm:"not null;default:0" json:"upstreamPort"`
	UpstreamHash     string `gorm:"not null;default:'';index" json:"-"`
	IdemKey          string `gorm:"not null;default:'';uniqueIndex:idx_forward_chain_idem,where:idem_key <> ''" json:"-"`
	// Op is the running operation (create | attach | relink | retarget |
	// move); OpStartedAt its start (0 = idle); LastOpAt the start of the last
	// finished one, so a client polling an operation knows when it ended.
	Op          string `gorm:"not null;default:''" json:"op"`
	OpStartedAt int64  `gorm:"not null;default:0;index" json:"opStartedAt"`
	LastOpAt    int64  `gorm:"not null;default:0" json:"lastOpAt"`
	CreatedTime int64  `gorm:"not null" json:"createdTime"`
	UpdatedTime int64  `gorm:"not null" json:"updatedTime"`
}

func (ForwardChain) TableName() string { return "forward_chain" }

// Chain copy states: pending copies belong to an unfinished create or tunnel
// change and are removed by the reconciler if that operation died.
const (
	ChainNodeActive  = "active"
	ChainNodePending = "pending"
)

type ForwardChainNode struct {
	ID              int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	ChainID         int64  `gorm:"not null;index" json:"chainId"`
	LandingServerID int64  `gorm:"not null;uniqueIndex:idx_forward_chain_node_port" json:"landingServerId"`
	NodeID          int64  `gorm:"not null;index" json:"nodeId"`
	Port            int    `gorm:"not null;uniqueIndex:idx_forward_chain_node_port" json:"port"`
	State           string `gorm:"not null;default:'active'" json:"state"`
	CreatedTime     int64  `gorm:"not null" json:"createdTime"`
	GuardPort       int    `gorm:"not null;default:0" json:"-"` // Reality guard loopback port (plan 127)
}

func (ForwardChainNode) TableName() string { return "forward_chain_node" }

// ChainAgentIDBase separates chain copy ids from landing node ids in the
// agent's desired state (both use the flvxl-<id> inbound tag).
const ChainAgentIDBase int64 = 1 << 40
