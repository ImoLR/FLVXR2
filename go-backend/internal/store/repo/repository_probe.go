package repo

import (
	"errors"
	"fmt"

	"go-backend/internal/store/model"
)

// Plan 187 公开探针: the few reads the public probe needs. Only the columns the
// page may show (plus id for the in-memory joins) are selected.

// ProbeNodeRow is a local node as the probe reads it.
type ProbeNodeRow struct {
	ID      int64
	Inx     int
	Name    string
	Region  string
	Version string
	// Plan 194: next renewal / expiry (ms, 0 = not set) and the raw renewal
	// cycle. The public page only ever gets the derived day count.
	ExpiryTime   int64
	RenewalCycle string
	// Plan 195: addresses, only to match a 救命版 machine with this node
	// (same machine running both agents). Never published.
	ServerIP   string
	ServerIPV4 string
	ServerIPV6 string
	ExtraIPs   string
}

func (r *Repository) ListProbeNodes() ([]ProbeNodeRow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var nodes []model.Node
	err := r.db.Select("id", "inx", "name", "region", "version", "expiry_time", "renewal_cycle", "server_ip", "server_ip_v4", "server_ip_v6", "extra_ips").
		Where("is_remote = ?", 0).
		Order("inx ASC, id ASC").
		Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	out := make([]ProbeNodeRow, 0, len(nodes))
	for _, n := range nodes {
		row := ProbeNodeRow{ID: n.ID, Inx: n.Inx, Name: n.Name, Region: n.Region, Version: n.Version.String, RenewalCycle: n.RenewalCycle.String,
			ServerIP: n.ServerIP, ServerIPV4: n.ServerIPV4.String, ServerIPV6: n.ServerIPV6.String, ExtraIPs: n.ExtraIPs.String}
		if n.ExpiryTime.Valid && n.ExpiryTime.Int64 > 0 {
			row.ExpiryTime = n.ExpiryTime.Int64
		}
		out = append(out, row)
	}
	return out, nil
}

// LatestNodeMetricTime is the newest stored metric of a node (ms, 0 = none):
// one index seek on idx_node_metric_node_time.
func (r *Repository) LatestNodeMetricTime(nodeID int64) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	var ts *int64
	err := r.db.Model(&model.NodeMetric{}).Select("MAX(timestamp)").Where("node_id = ?", nodeID).Scan(&ts).Error
	if err != nil || ts == nil {
		return 0, err
	}
	return *ts, nil
}

// NodeMetricBucket is one averaged chart point.
type NodeMetricBucket struct {
	Timestamp   int64   `gorm:"column:timestamp"`
	CPUUsage    float64 `gorm:"column:cpu_usage"`
	MemUsage    float64 `gorm:"column:mem_usage"`
	DiskUsage   float64 `gorm:"column:disk_usage"`
	NetInSpeed  float64 `gorm:"column:net_in_speed"`
	NetOutSpeed float64 `gorm:"column:net_out_speed"`
	Load1       float64 `gorm:"column:load1"`
	TCPConns    float64 `gorm:"column:tcp_conns"`
	UDPConns    float64 `gorm:"column:udp_conns"`
}

// NodeMetricBuckets averages node_metric rows into step-ms buckets (≤ limit
// points), in SQL: the probe detail and the 监控 detail charts never pull raw
// 1 s rows (plan 188). Works on SQLite and PostgreSQL (integer division of
// bigint timestamps).
func (r *Repository) NodeMetricBuckets(nodeID, startMs, endMs, stepMs int64, limit int) ([]NodeMetricBucket, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	if stepMs < 1000 {
		stepMs = 1000
	}
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	out := make([]NodeMetricBucket, 0, limit)
	err := r.db.Model(&model.NodeMetric{}).
		Select(fmt.Sprintf("CAST(timestamp / %d * %d AS BIGINT) AS timestamp, "+
			"AVG(cpu_usage) AS cpu_usage, AVG(mem_usage) AS mem_usage, AVG(disk_usage) AS disk_usage, "+
			"AVG(net_in_speed) AS net_in_speed, AVG(net_out_speed) AS net_out_speed, AVG(load1) AS load1, "+
			"AVG(tcp_conns) AS tcp_conns, AVG(udp_conns) AS udp_conns", stepMs, stepMs)).
		Where("node_id = ? AND timestamp >= ? AND timestamp <= ?", nodeID, startMs, endMs).
		Group(fmt.Sprintf("timestamp / %d", stepMs)).
		Order("timestamp ASC").
		Limit(limit + 2).
		Scan(&out).Error
	return out, err
}
