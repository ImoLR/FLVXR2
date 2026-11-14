package model

// Plan 255 「线路监测」: admin-built comparison charts. A chart holds 1–8 lines;
// a line is one source node → target (another node or a custom host) TCP
// latency series, measured by the panel every 30 s with the agent's existing
// TcpPing command. Samples keep 30 days of raw 30 s slots.

// LineMonitorChart is one 监测图.
type LineMonitorChart struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"type:varchar(100);not null" json:"name"`
	Sort        int    `gorm:"not null;default:0" json:"sort"`
	CreatedTime int64  `gorm:"column:created_time;not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"column:updated_time;not null;default:0" json:"updatedTime"`
}

func (LineMonitorChart) TableName() string { return "line_monitor_chart" }

// LineMonitorLine is one line of a chart. TargetNodeID > 0 = an FLVX node
// target; Host empty then follows that node's current address. TargetNodeID 0
// = a custom host:port.
type LineMonitorLine struct {
	ID           int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	ChartID      int64  `gorm:"column:chart_id;not null;index" json:"chartId"`
	SourceNodeID int64  `gorm:"column:source_node_id;not null" json:"sourceNodeId"`
	TargetNodeID int64  `gorm:"column:target_node_id;not null;default:0" json:"targetNodeId"`
	Host         string `gorm:"type:varchar(255);not null;default:''" json:"host"`
	Port         int    `gorm:"not null" json:"port"`
	Label        string `gorm:"type:varchar(100);not null;default:''" json:"label"`
	// Color is the palette index, fixed at creation so the other lines keep
	// their colours when one is removed.
	Color       int   `gorm:"not null;default:0" json:"color"`
	Paused      int   `gorm:"not null;default:0" json:"paused"`
	Sort        int   `gorm:"not null;default:0" json:"sort"`
	CreatedTime int64 `gorm:"column:created_time;not null;default:0" json:"createdTime"`
	UpdatedTime int64 `gorm:"column:updated_time;not null;default:0" json:"updatedTime"`

	// Plan 255 part 2 「检测端口」.
	// PortMode (node targets): auto = the panel opens a check port on the
	// target through its agent (Port is then the reserved check port);
	// existing / manual = a port that already listens there ("" = manual).
	PortMode string `gorm:"column:port_mode;type:varchar(16);not null;default:''" json:"portMode"`
	// CheckWindow / CheckAllow (custom targets): idle window (hours) and the
	// 「仅允许来源节点连接」 switch of the generated 开启命令.
	CheckWindow int `gorm:"column:check_window;not null;default:0" json:"checkWindow"`
	CheckAllow  int `gorm:"column:check_allow;not null;default:0" json:"checkAllow"`
	// CheckState: last verify of an auto check port from this line's source:
	// ok | unreachable | refused | unverified | pending | unsupported | busy |
	// failed ("" = never checked). CheckMsg = detail, CheckAt = unix ms.
	CheckState string `gorm:"column:check_state;type:varchar(16);not null;default:''" json:"checkState"`
	CheckMsg   string `gorm:"column:check_msg;type:varchar(255);not null;default:''" json:"checkMsg"`
	CheckAt    int64  `gorm:"column:check_at;not null;default:0" json:"checkAt"`
}

func (LineMonitorLine) TableName() string { return "line_monitor_line" }

// Line port modes (plan 255 part 2).
const (
	LinePortAuto     = "auto"
	LinePortExisting = "existing"
	LinePortManual   = "manual"
)

// Line check states.
const (
	LineCheckOK          = "ok"          // the source connected to the check port
	LineCheckUnreachable = "unreachable" // open on the agent, the source cannot connect (vendor firewall / NAT)
	LineCheckRefused     = "refused"     // the source got a refusal (nothing listens there)
	LineCheckUnverified  = "unverified"  // open, but the source could not be asked (offline / no answer)
	LineCheckPending     = "pending"     // the target agent is offline: opened on its next reconnect
	LineCheckUnsupported = "unsupported" // the target agent has no MonitorPort (needs OTA)
	LineCheckBusy        = "busy"        // the agent refused every port tried
	LineCheckFailed      = "failed"      // the agent could not open it (detail in CheckMsg)
)

// LineMonitorPort is a check port the panel wants open on one of its nodes
// (plan 255 part 2). Every row — open or closing — reserves (node, port) in
// all port allocators, so no rule / tunnel / landing node can take it. A row
// is referenced by the auto-mode lines with that target node and port; with
// no line left it turns closing (close intent, stored in the same
// transaction as the line change) and is deleted once the agent has
// acknowledged a full sync without it.
type LineMonitorPort struct {
	ID     int64 `gorm:"primaryKey;autoIncrement" json:"id"`
	NodeID int64 `gorm:"column:node_id;not null;uniqueIndex:idx_line_monitor_port_node_port" json:"nodeId"`
	Port   int   `gorm:"not null;uniqueIndex:idx_line_monitor_port_node_port" json:"port"`
	// State: open (wanted) | closing (待关闭).
	State string `gorm:"type:varchar(16);not null;default:'open'" json:"state"`
	// AgentState: what the agent answered for it last: "" (not yet) | open |
	// busy | refused | error; AgentMsg = its reason.
	AgentState  string `gorm:"column:agent_state;type:varchar(16);not null;default:''" json:"agentState"`
	AgentMsg    string `gorm:"column:agent_msg;type:varchar(255);not null;default:''" json:"agentMsg"`
	CreatedTime int64  `gorm:"column:created_time;not null;default:0" json:"createdTime"`
	UpdatedTime int64  `gorm:"column:updated_time;not null;default:0" json:"updatedTime"`
}

func (LineMonitorPort) TableName() string { return "line_monitor_port" }

// Check port row states.
const (
	LineMonitorPortOpen    = "open"
	LineMonitorPortClosing = "closing"
)

// Line sample status values. A slot the panel could not measure (source
// offline, command error, panel down) has no row at all.
const (
	LineSampleOK   = 1 // first attempt connected
	LineSampleLoss = 2 // first attempt failed, the retry connected (Ms10 = retry)
	LineSampleDown = 3 // both attempts failed
	// LineSampleClosed (plan 255 part 2): both attempts refused — the target
	// answers but nothing listens there (「端口未开启」, e.g. an expired check
	// port). Not 断流, not counted for 可用率.
	LineSampleClosed = 4
)

// LineMonitorSample is one measured 30 s slot. ID packs the (line_id, ts)
// key (LineSampleKey): on SQLite an INTEGER primary key is the table's own
// rowid, so the rows are stored in (line_id, ts) order without a second
// index (measured: 40 lines × 30 days = 76 MB instead of 123 MB with a
// composite key + its index). Every range read / prune is an ID range.
type LineMonitorSample struct {
	ID     int64 `gorm:"column:id;primaryKey;autoIncrement:false"`
	LineID int64 `gorm:"column:line_id;not null"`
	TS     int64 `gorm:"column:ts;not null"`
	Status int   `gorm:"column:status;not null"`
	// Ms10 = latency in tenths of a millisecond (0 for down).
	Ms10 int `gorm:"column:ms10;not null;default:0"`
}

// LineSampleKey is the packed (line_id, ts) primary key of a sample.
func LineSampleKey(lineID, ts int64) int64 {
	if ts < 0 {
		ts = 0
	}
	if ts > 1<<32-1 {
		ts = 1<<32 - 1
	}
	return lineID<<32 | ts
}

func (LineMonitorSample) TableName() string { return "line_monitor_sample" }
