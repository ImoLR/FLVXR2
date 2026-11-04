package repo

import (
	"encoding/json"
	"log"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 211: 节点总连接数 — every TCP socket of a landing node's port in any
// state (入站+出站, incl. TIME_WAIT / CLOSE_WAIT; Core port
// max_total_connections), next to the node's 入站 limit (MaxConnections).
// Whichever is hit first (together with the user's 总入站连接数 and the
// machine's 整机兜底) refuses NEW connections; existing ones stay.
const (
	// LandingDefaultNodeTotal is the default of new servers and the value
	// every existing server / node got on migration (user 11-05).
	LandingDefaultNodeTotal = 400
	// MaxLandingNodeTotal bounds the setting (the Core takes int64).
	MaxLandingNodeTotal = 10_000_000
	// LandingNodeTotalAgentVersion: native agents reporting landing_conn_mode
	// >= 2 apply nodes[].max_total_connections next to an inbound limit and
	// report the per-node TCP breakdown.
	LandingNodeTotalAgentVersion = 2
)

// NativeNodeTotalSupported: the native agent applies both node limits at once.
func NativeNodeTotalSupported(agentVersion int) bool {
	return agentVersion >= LandingNodeTotalAgentVersion
}

// SharedNodeTotalSupported: the share's Helper applies max_total_connections
// next to the inbound limit (contract landing-node-total-v1).
func SharedNodeTotalSupported(s *model.LandingServer) bool {
	return s != nil && s.Source == "shared" && SharedLandingCaps(s).NodeTotal.Supports(landingcustom.NodeTotalVersion)
}

// LandingNodeTotalSupported answers for either source (agentVersion is the
// native agent's landing_conn_mode, ignored for shared servers).
func LandingNodeTotalSupported(s *model.LandingServer, agentVersion int) bool {
	if s == nil {
		return false
	}
	if s.Source == "shared" {
		return SharedNodeTotalSupported(s)
	}
	return NativeNodeTotalSupported(agentVersion)
}

// LandingSentLimits is what one node's runtime receives.
type LandingSentLimits struct {
	// MaxConnections / Inbound: the max_connections field and whether it goes
	// with conn_mode "inbound" (otherwise the runtime counts it 入站+出站).
	MaxConnections int
	Inbound        bool
	// MaxTotal: max_total_connections, only for runtimes that apply both.
	MaxTotal int
	// TotalPending: the node total is set but not applied (inbound limit set
	// too and the runtime cannot take both) — the UI says 「待升级」.
	TotalPending bool
}

// LandingSendLimits maps a node's 入站 limit and 节点总连接数 to the runtime.
// inboundMode = the runtime counts the node's limit as 入站 (conn_mode);
// totalCapable = it also takes max_total_connections. Without a total the
// result is exactly what was sent before plan 211.
//   - a capable runtime gets both fields;
//   - an inbound limit of 0 lets the total travel the old way (max_connections
//     without conn_mode = Core port total) — every agent / Helper applies it;
//   - a runtime counting every limit 入站+出站 anyway gets the smaller one;
//   - otherwise the inbound limit stays and the total waits for an upgrade.
func LandingSendLimits(inbound, total int, inboundMode, totalCapable bool) LandingSentLimits {
	inbound, total = max(inbound, 0), max(total, 0)
	switch {
	case total == 0:
		return LandingSentLimits{MaxConnections: inbound, Inbound: inboundMode}
	case totalCapable:
		return LandingSentLimits{MaxConnections: inbound, Inbound: inboundMode, MaxTotal: total}
	case !inboundMode:
		return LandingSentLimits{MaxConnections: minPositiveInt(inbound, total)}
	case inbound == 0:
		return LandingSentLimits{MaxConnections: total}
	default:
		return LandingSentLimits{MaxConnections: inbound, Inbound: true, TotalPending: true}
	}
}

func minPositiveInt(a, b int) int {
	switch {
	case a <= 0:
		return max(b, 0)
	case b <= 0:
		return a
	default:
		return min(a, b)
	}
}

// landingNodeTotalMigration runs once, right after AutoMigrate added
// landing_node.max_total_connections to an existing database:
//   - every landing server: 新节点默认 节点总连接数 = 400; 服务器总连接数 → 0
//     (hidden, no longer enforced by FLVX; a shared server's number was the
//     妙妙屋 share cap, which the Helper keeps enforcing on its own);
//   - every landing node: 节点总连接数 = 400;
//   - shared servers: node / default 入站 limits equal to the share cap were
//     copies made by the share sync (「800 (入站)」) → 0 (不限).
//
// The old values are logged for the release notes (undo by hand if needed).
func landingNodeTotalMigration(db *gorm.DB) error {
	var servers []model.LandingServer
	if err := db.Select("id", "source", "name", "max_connections", "default_max_connections", "shared_caps").Find(&servers).Error; err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, s := range servers {
			capConns := 0
			if s.Source == "shared" && s.SharedCaps != "" {
				var caps landingcustom.Share
				if json.Unmarshal([]byte(s.SharedCaps), &caps) == nil {
					capConns = caps.MaxConnections
				}
			}
			var copied int64
			if capConns > 0 {
				res := tx.Model(&model.LandingNode{}).Where("landing_server_id = ? AND max_connections = ?", s.ID, capConns).Update("max_connections", 0)
				if res.Error != nil {
					return res.Error
				}
				copied = res.RowsAffected
			}
			updates := map[string]any{"default_max_total_connections": LandingDefaultNodeTotal, "max_connections": 0}
			defaultInbound := s.DefaultMaxConnections
			if capConns > 0 && s.DefaultMaxConnections == capConns {
				updates["default_max_connections"], defaultInbound = 0, 0
			}
			if err := tx.Model(&model.LandingServer{}).Where("id = ?", s.ID).Updates(updates).Error; err != nil {
				return err
			}
			log.Printf("[landing-node-total] server %d %q (%s): max_connections %d→0, default_max_connections %d→%d, share cap %d, %d node 入站 copies→0", s.ID, s.Name, s.Source, s.MaxConnections, s.DefaultMaxConnections, defaultInbound, capConns, copied)
		}
		res := tx.Model(&model.LandingNode{}).Where("1 = 1").Update("max_total_connections", LandingDefaultNodeTotal)
		if res.Error != nil {
			return res.Error
		}
		log.Printf("[landing-node-total] %d landing nodes: max_total_connections → %d", res.RowsAffected, LandingDefaultNodeTotal)
		return nil
	})
}
