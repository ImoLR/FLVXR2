package model

// TrafficDaily is the per-day billed traffic rollup (plan 212). Rows are added in
// the flow-upload transaction, so they move together with user.in_flow/out_flow
// and user_server_quota.used_in/used_out and never count an upload twice.
//
// Day is the UTC+8 calendar date as YYYYMMDD. ServerKind is "total" (ServerRefID
// 0: the user's billed total, the same bytes as user.in_flow/out_flow), "node" (an
// FLVX node: entry, relay, exit or native landing) or "landing" (a shared landing
// server) — the per-server rows carry the same bytes as user_server_quota, so a
// flow crossing several servers is in each of them.
type TrafficDaily struct {
	ID          int64  `gorm:"primaryKey;autoIncrement" json:"-"`
	Day         int64  `gorm:"not null;uniqueIndex:idx_traffic_daily_key,priority:1" json:"day"`
	UserID      int64  `gorm:"not null;uniqueIndex:idx_traffic_daily_key,priority:2" json:"userId"`
	ServerKind  string `gorm:"type:varchar(16);not null;uniqueIndex:idx_traffic_daily_key,priority:3" json:"serverKind"`
	ServerRefID int64  `gorm:"not null;default:0;uniqueIndex:idx_traffic_daily_key,priority:4" json:"serverRefId"`
	InBytes     int64  `gorm:"not null;default:0" json:"inBytes"`
	OutBytes    int64  `gorm:"not null;default:0" json:"outBytes"`
	UpdatedTime int64  `gorm:"not null;default:0" json:"-"`
}

func (TrafficDaily) TableName() string { return "traffic_daily" }
