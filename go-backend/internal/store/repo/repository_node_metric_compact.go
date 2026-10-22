package repo

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// node_metric tiering (plan 135). Agents report once per second per node and
// every report is stored. The monitor charts read raw rows only for windows
// of up to 1 h (15 min / 1 h); the 6 h and 24 h windows are averaged into
// 500 buckets of 43 s / 173 s by GetNodeMetrics. Rows older than one hour are
// therefore replaced by one averaged row per node per 30 s, which keeps every
// chart bucket filled while storing ~14x fewer rows.

// CompactNodeMetrics replaces the rows of [fromMs, toMs) with one averaged row
// per node and bucketMs (timestamp = bucket middle). It writes nothing when
// the range is already compact. Aggregates match GetNodeMetrics (AVG, MAX
// for uptime), so the downsampled 6 h / 24 h charts read the same values.
func (r *Repository) CompactNodeMetrics(fromMs, toMs, bucketMs int64) (deleted int64, err error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	if bucketMs <= 0 || toMs <= fromMs {
		return 0, nil
	}
	// bucketMs is an int64 inlined as a literal (like GetNodeMetrics): with a
	// bind parameter PostgreSQL cannot match the SELECT bucket expression to
	// the GROUP BY one.
	bucket := fmt.Sprintf("(timestamp / %d)", bucketMs)
	var dup []int64
	if err := r.db.Raw(`SELECT node_id FROM node_metric WHERE timestamp >= ? AND timestamp < ?
		GROUP BY node_id, `+bucket+` HAVING COUNT(*) > 1 LIMIT 1`, fromMs, toMs).
		Scan(&dup).Error; err != nil {
		return 0, err
	}
	if len(dup) == 0 {
		return 0, nil
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var maxID int64
		if err := tx.Raw(`SELECT COALESCE(MAX(id), 0) FROM node_metric`).Scan(&maxID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO node_metric (node_id, timestamp, cpu_usage, mem_usage, disk_usage,
				net_in_bytes, net_out_bytes, net_in_speed, net_out_speed, load1, load5, load15,
				tcp_conns, udp_conns, uptime, period_rx, period_tx)
			SELECT node_id, `+bucket+fmt.Sprintf(" * %d + %d", bucketMs, bucketMs/2)+`,
				AVG(cpu_usage), AVG(mem_usage), AVG(disk_usage),
				CAST(AVG(net_in_bytes) AS BIGINT), CAST(AVG(net_out_bytes) AS BIGINT),
				CAST(AVG(net_in_speed) AS BIGINT), CAST(AVG(net_out_speed) AS BIGINT),
				AVG(load1), AVG(load5), AVG(load15),
				CAST(AVG(tcp_conns) AS BIGINT), CAST(AVG(udp_conns) AS BIGINT),
				MAX(uptime), MAX(period_rx), MAX(period_tx)
			FROM node_metric
			WHERE timestamp >= ? AND timestamp < ? AND id <= ?
			GROUP BY node_id, `+bucket,
			fromMs, toMs, maxID).Error; err != nil {
			return err
		}
		res := tx.Exec(`DELETE FROM node_metric WHERE timestamp >= ? AND timestamp < ? AND id <= ?`, fromMs, toMs, maxID)
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		return nil
	})
	return deleted, err
}

// PruneNodeMetricsBatch deletes at most limit rows older than olderThanMs and
// returns how many it deleted (callers loop with a pause, keeping each write
// lock short).
func (r *Repository) PruneNodeMetricsBatch(olderThanMs int64, limit int) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	if limit <= 0 {
		limit = 2000
	}
	res := r.db.Exec(`DELETE FROM node_metric WHERE id IN (
		SELECT id FROM node_metric WHERE timestamp < ? ORDER BY timestamp LIMIT ?)`, olderThanMs, limit)
	return res.RowsAffected, res.Error
}

// OldestNodeMetricTimestamp returns the oldest stored timestamp (0 if none).
func (r *Repository) OldestNodeMetricTimestamp() (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	var ts *int64
	if err := r.db.Raw(`SELECT MIN(timestamp) FROM node_metric`).Scan(&ts).Error; err != nil {
		return 0, err
	}
	if ts == nil {
		return 0, nil
	}
	return *ts, nil
}
