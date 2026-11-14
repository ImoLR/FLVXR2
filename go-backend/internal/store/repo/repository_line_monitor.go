package repo

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	gsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"go-backend/internal/store/model"
)

// Plan 255 「线路监测」 repository: charts, lines, samples.

// openLineMonitorReader opens a second, read-only connection on a SQLite file
// for the long 线路监测 range reads: in WAL mode a reader neither waits for the
// main (single) connection nor blocks it. nil (→ main connection) for
// in-memory databases or when the open fails.
func openLineMonitorReader(path string) *gorm.DB {
	if strings.TrimSpace(path) == "" || strings.Contains(path, ":memory:") || strings.Contains(path, "mode=memory") {
		return nil
	}
	dsn := "file:" + path + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	db, err := gorm.Open(gsqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		log.Printf("[line-monitor] read-only connection not opened (main connection used): %v", err)
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	// Idle reader closes after a while (no long-lived snapshot / file handle).
	sqlDB.SetConnMaxIdleTime(2 * time.Minute)
	return db
}

func (r *Repository) lineReadDB() *gorm.DB {
	if r.lineRead != nil {
		return r.lineRead
	}
	return r.db
}

// ListLineMonitorCharts returns every chart in display order.
func (r *Repository) ListLineMonitorCharts() ([]model.LineMonitorChart, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.LineMonitorChart
	err := r.db.Order("sort ASC, id ASC").Find(&out).Error
	return out, err
}

// ListLineMonitorLines returns every line (all charts) in display order.
func (r *Repository) ListLineMonitorLines() ([]model.LineMonitorLine, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.LineMonitorLine
	err := r.db.Order("chart_id ASC, sort ASC, id ASC").Find(&out).Error
	return out, err
}

// GetLineMonitorChart returns one chart (nil when missing).
func (r *Repository) GetLineMonitorChart(id int64) (*model.LineMonitorChart, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var c model.LineMonitorChart
	err := r.db.Where("id = ?", id).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListLineMonitorChartLines returns the lines of one chart in display order.
func (r *Repository) ListLineMonitorChartLines(chartID int64) ([]model.LineMonitorLine, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.LineMonitorLine
	err := r.db.Where("chart_id = ?", chartID).Order("sort ASC, id ASC").Find(&out).Error
	return out, err
}

// SaveLineMonitorChart creates (chart.ID == 0) or updates a chart with its full
// line list in one transaction: lines with an ID that belongs to the chart are
// updated, lines without an ID are created, the chart's other lines are
// removed (their IDs are returned so the caller can delete their samples).
// lineCap limits the lines of all charts together (checked inside the
// transaction).
//
// Plan 255 part 2: auto-mode lines get their check port here (kept while the
// target and mode stay, shared with another auto line of the same node, or
// picked from the node's rule range; busy = ports agents reported busy), and
// the check-port rows of every touched node are reconciled in the same
// transaction (a port no line uses any more turns closing).
func (r *Repository) SaveLineMonitorChart(chart *model.LineMonitorChart, lines []model.LineMonitorLine, lineCap int, busy map[int64]map[int]bool) (res LineMonitorSaveResult, err error) {
	if r == nil || r.db == nil {
		return res, errors.New("repository not initialized")
	}
	if chart == nil {
		return res, errors.New("监测图不存在")
	}
	now := time.Now().UnixMilli()
	var removed, verify []int64
	var touched []int64
	err = r.db.Transaction(func(tx *gorm.DB) error {
		removed, verify, touched = nil, nil, nil
		if chart.ID == 0 {
			var maxSort struct{ M int }
			if err := tx.Model(&model.LineMonitorChart{}).Select("COALESCE(MAX(sort), 0) AS m").Scan(&maxSort).Error; err != nil {
				return err
			}
			chart.Sort = maxSort.M + 1
			chart.CreatedTime, chart.UpdatedTime = now, now
			if err := tx.Create(chart).Error; err != nil {
				return err
			}
		} else {
			res := tx.Model(&model.LineMonitorChart{}).Where("id = ?", chart.ID).Updates(map[string]interface{}{"name": chart.Name, "updated_time": now})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errors.New("监测图不存在")
			}
		}
		var existing []model.LineMonitorLine
		if err := tx.Where("chart_id = ?", chart.ID).Find(&existing).Error; err != nil {
			return err
		}
		byID := make(map[int64]model.LineMonitorLine, len(existing))
		for _, l := range existing {
			byID[l.ID] = l
			if l.PortMode == model.LinePortAuto {
				touched = append(touched, l.TargetNodeID)
			}
		}
		// Check ports of the auto lines (plan 255 part 2).
		picker := &autoPortPicker{tx: tx, busy: busy, assigned: map[int64]int{}}
		for i := range lines {
			l := &lines[i]
			if l.PortMode != model.LinePortAuto || l.TargetNodeID <= 0 {
				continue
			}
			touched = append(touched, l.TargetNodeID)
			if old, ok := byID[l.ID]; ok && l.ID > 0 && old.PortMode == model.LinePortAuto && old.TargetNodeID == l.TargetNodeID &&
				old.Port > 0 && (l.Port == 0 || l.Port == old.Port) {
				l.Port = old.Port
				if picker.assigned[l.TargetNodeID] == 0 {
					picker.assigned[l.TargetNodeID] = old.Port
				}
				continue
			}
			port, err := picker.pick(l.TargetNodeID)
			if err != nil {
				return fmt.Errorf("第 %d 条线路：%v", i+1, err)
			}
			l.Port = port
		}
		keep := make(map[int64]bool, len(lines))
		for i := range lines {
			l := &lines[i]
			l.ChartID = chart.ID
			l.Sort = i + 1
			l.UpdatedTime = now
			if l.ID > 0 {
				old, ok := byID[l.ID]
				if !ok {
					return fmt.Errorf("线路 %d 不属于该监测图", l.ID)
				}
				keep[l.ID] = true
				l.CreatedTime = old.CreatedTime
				l.Color = old.Color
				upd := map[string]interface{}{
					"source_node_id": l.SourceNodeID, "target_node_id": l.TargetNodeID, "host": l.Host, "port": l.Port,
					"label": l.Label, "paused": l.Paused, "sort": l.Sort, "updated_time": now,
					"port_mode": l.PortMode, "check_window": l.CheckWindow, "check_allow": l.CheckAllow,
				}
				if old.PortMode != l.PortMode || old.TargetNodeID != l.TargetNodeID || old.Port != l.Port || old.SourceNodeID != l.SourceNodeID || old.Host != l.Host {
					for k, v := range lineCheckReset(now) {
						upd[k] = v
					}
					l.CheckState, l.CheckMsg = "", ""
					if l.PortMode == model.LinePortAuto {
						verify = append(verify, l.ID)
					}
				} else {
					l.CheckState, l.CheckMsg, l.CheckAt = old.CheckState, old.CheckMsg, old.CheckAt
				}
				if err := tx.Model(&model.LineMonitorLine{}).Where("id = ?", l.ID).Updates(upd).Error; err != nil {
					return err
				}
			}
		}
		for id := range byID {
			if !keep[id] {
				removed = append(removed, id)
			}
		}
		if len(removed) > 0 {
			if err := tx.Where("id IN ?", removed).Delete(&model.LineMonitorLine{}).Error; err != nil {
				return err
			}
		}
		// Colours: new lines get the first palette slots the kept lines don't use.
		used := make(map[int]bool)
		for _, l := range lines {
			if l.ID > 0 {
				used[l.Color] = true
			}
		}
		next := 0
		for i := range lines {
			l := &lines[i]
			if l.ID > 0 {
				continue
			}
			for used[next] {
				next++
			}
			l.Color = next
			used[next] = true
			l.CreatedTime = now
			if err := tx.Create(l).Error; err != nil {
				return err
			}
			if l.PortMode == model.LinePortAuto {
				verify = append(verify, l.ID)
			}
		}
		if err := reconcileLineMonitorPortsTx(tx, touched, now); err != nil {
			return err
		}
		if lineCap > 0 {
			var total int64
			if err := tx.Model(&model.LineMonitorLine{}).Count(&total).Error; err != nil {
				return err
			}
			if total > int64(lineCap) {
				return fmt.Errorf("线路总数不能超过 %d 条（所有监测图合计）", lineCap)
			}
		}
		return nil
	})
	if err != nil {
		return LineMonitorSaveResult{}, err
	}
	return LineMonitorSaveResult{Removed: removed, Nodes: uniqueIDs(touched), Verify: verify}, nil
}

// LineMonitorSaveResult: what a chart save / delete changed.
type LineMonitorSaveResult struct {
	Removed []int64 // lines removed (their samples are deleted by the caller)
	Nodes   []int64 // target nodes whose check ports may have changed (push a sync)
	Verify  []int64 // auto lines with a new / changed check port (verify from the source)
}

// DeleteLineMonitorChart removes a chart and its lines; returns the line IDs
// (their samples are deleted by the caller in batches).
func (r *Repository) DeleteLineMonitorChart(id int64) (LineMonitorSaveResult, error) {
	if r == nil || r.db == nil {
		return LineMonitorSaveResult{}, errors.New("repository not initialized")
	}
	var ids, nodes []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		ids, nodes = nil, nil
		if err := tx.Model(&model.LineMonitorLine{}).Where("chart_id = ?", id).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.LineMonitorLine{}).Where("chart_id = ? AND port_mode = ?", id, model.LinePortAuto).
			Distinct("target_node_id").Pluck("target_node_id", &nodes).Error; err != nil {
			return err
		}
		if err := tx.Where("chart_id = ?", id).Delete(&model.LineMonitorLine{}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ?", id).Delete(&model.LineMonitorChart{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("监测图不存在")
		}
		// Close intent of the check ports only this chart used (same transaction).
		return reconcileLineMonitorPortsTx(tx, nodes, time.Now().UnixMilli())
	})
	if err != nil {
		return LineMonitorSaveResult{}, err
	}
	return LineMonitorSaveResult{Removed: ids, Nodes: uniqueIDs(nodes)}, nil
}

// ReorderLineMonitorCharts stores the display order (ids in order; unknown ids
// are ignored, charts not listed keep their place after the listed ones).
func (r *Repository) ReorderLineMonitorCharts(ids []int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.LineMonitorChart{}).Where("id = ?", id).Update("sort", i+1).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SetLineMonitorLinePaused pauses / resumes one line.
func (r *Repository) SetLineMonitorLinePaused(id int64, paused bool) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	v := 0
	if paused {
		v = 1
	}
	res := r.db.Model(&model.LineMonitorLine{}).Where("id = ?", id).Updates(map[string]interface{}{"paused": v, "updated_time": time.Now().UnixMilli()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("线路不存在")
	}
	return nil
}

// InsertLineMonitorSamples writes one batch (one statement; a slot already
// stored is kept). The packed key is filled from (LineID, TS).
func (r *Repository) InsertLineMonitorSamples(samples []model.LineMonitorSample) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if len(samples) == 0 {
		return nil
	}
	for i := range samples {
		samples[i].ID = model.LineSampleKey(samples[i].LineID, samples[i].TS)
	}
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).CreateInBatches(samples, 200).Error
}

// DeleteLineMonitorSamplesBefore deletes at most ~limit samples of one line
// older than before (ts < before), oldest first: one primary-key range per
// batch. Returns the number of rows deleted (< limit = nothing older left).
func (r *Repository) DeleteLineMonitorSamplesBefore(lineID, before int64, limit int) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	if limit <= 0 {
		limit = 2000
	}
	lo, hi := model.LineSampleKey(lineID, 0), model.LineSampleKey(lineID, before)
	if before > 1<<32-1 {
		hi = model.LineSampleKey(lineID+1, 0)
	}
	var edge []int64
	if err := r.db.Model(&model.LineMonitorSample{}).Where("id >= ? AND id < ?", lo, hi).
		Order("id ASC").Offset(limit-1).Limit(1).Pluck("id", &edge).Error; err != nil {
		return 0, err
	}
	if len(edge) == 1 {
		hi = edge[0] + 1
	}
	res := r.db.Where("id >= ? AND id < ?", lo, hi).Delete(&model.LineMonitorSample{})
	return res.RowsAffected, res.Error
}

// LineMonitorSampleLineIDs returns the distinct line IDs that have samples
// (one key seek per line, never a full scan).
func (r *Repository) LineMonitorSampleLineIDs() ([]int64, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []int64
	next := int64(0)
	for i := 0; i < 10000; i++ {
		var ids []int64
		if err := r.db.Model(&model.LineMonitorSample{}).Where("id >= ?", model.LineSampleKey(next, 0)).
			Order("id ASC").Limit(1).Pluck("id", &ids).Error; err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			break
		}
		line := ids[0] >> 32
		out = append(out, line)
		next = line + 1
	}
	return out, nil
}

// LineMonitorSampleRow is one row of a range read.
type LineMonitorSampleRow struct {
	TS     int64
	Status int
	Ms10   int
}

// ReadLineMonitorSamples streams one line's samples with from <= ts < to in ts
// order to fn (no slice of the whole range is built): one primary-key range,
// on the read-only connection on SQLite.
func (r *Repository) ReadLineMonitorSamples(lineID, from, to int64, fn func(LineMonitorSampleRow)) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	rows, err := r.lineReadDB().Model(&model.LineMonitorSample{}).Select("ts, status, ms10").
		Where("id >= ? AND id < ?", model.LineSampleKey(lineID, from), model.LineSampleKey(lineID, to)).Order("id ASC").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row LineMonitorSampleRow
		if err := rows.Scan(&row.TS, &row.Status, &row.Ms10); err != nil {
			return err
		}
		fn(row)
	}
	return rows.Err()
}

// ListLineMonitorNodeRecords returns every node as a NodeRecord (pickers and
// target address resolution), in the 节点 page order.
func (r *Repository) ListLineMonitorNodeRecords() ([]*model.NodeRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var nodes []model.Node
	if err := r.db.Order("inx ASC, id ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}
	out := make([]*model.NodeRecord, 0, len(nodes))
	for i := range nodes {
		out = append(out, nodeRecordFromModel(&nodes[i]))
	}
	return out, nil
}

// LineMonitorPortHint is one port the panel knows listens on a node.
type LineMonitorPortHint struct {
	Port  int    `json:"port"`
	Kind  string `json:"kind"` // tunnel | landing | rule
	Label string `json:"label"`
}

// LineMonitorPortHints lists the ports the panel configured to listen on a
// node: tunnel relay ports (hop / exit; the 60 s path rounds already TcpPing
// these), native landing inbounds, rule entry ports. Running items only,
// deduplicated by port, at most max.
func (r *Repository) LineMonitorPortHints(nodeID int64, max int) ([]LineMonitorPortHint, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	if max <= 0 {
		max = 20
	}
	out := make([]LineMonitorPortHint, 0, max)
	seen := make(map[int]bool)
	add := func(port int, kind, label string) {
		if port <= 0 || port > 65535 || seen[port] || len(out) >= max {
			return
		}
		seen[port] = true
		out = append(out, LineMonitorPortHint{Port: port, Kind: kind, Label: label})
	}
	var tunnels []struct {
		Port int
		Name string
	}
	if err := r.db.Table("chain_tunnel AS c").Select("c.port AS port, t.name AS name").
		Joins("JOIN tunnel t ON t.id = c.tunnel_id").
		Where("c.node_id = ? AND c.chain_type IN ? AND c.port > 0 AND t.status = 1", nodeID, []string{"2", "3"}).
		Order("c.port ASC").Limit(max).Scan(&tunnels).Error; err != nil {
		return nil, err
	}
	for _, t := range tunnels {
		add(t.Port, "tunnel", "隧道 "+t.Name)
	}
	var landing []struct {
		Port     int
		Protocol string
	}
	if err := r.db.Table("landing_node AS n").Select("n.port AS port, n.protocol AS protocol").
		Joins("JOIN landing_server s ON s.id = n.landing_server_id").
		Where("s.node_id = ? AND s.node_id > 0 AND n.status = ? AND n.port > 0", nodeID, "active").
		Order("n.port ASC").Limit(max).Scan(&landing).Error; err != nil {
		return nil, err
	}
	for _, l := range landing {
		add(l.Port, "landing", "落地 "+l.Protocol)
	}
	var rules []struct {
		Port int
		Name string
	}
	if err := r.db.Table("forward_port AS p").Select("p.port AS port, f.name AS name").
		Joins("JOIN forward f ON f.id = p.forward_id").
		Where("p.node_id = ? AND p.port > 0 AND f.status = 1", nodeID).
		Order("p.port ASC").Limit(max).Scan(&rules).Error; err != nil {
		return nil, err
	}
	for _, f := range rules {
		add(f.Port, "rule", "规则 "+f.Name)
	}
	return out, nil
}
