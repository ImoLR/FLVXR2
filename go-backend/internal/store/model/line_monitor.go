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
}

func (LineMonitorLine) TableName() string { return "line_monitor_line" }

// Line sample status values. A slot the panel could not measure (source
// offline, command error, panel down) has no row at all.
const (
	LineSampleOK   = 1 // first attempt connected
	LineSampleLoss = 2 // first attempt failed, the retry connected (Ms10 = retry)
	LineSampleDown = 3 // both attempts failed
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
