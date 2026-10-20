package ws

import (
	"encoding/json"
	"fmt"
	"time"

	"go-backend/internal/store/repo"
)

// Whole-machine connection count and machine protection (plan 118). Agents that
// announce ?machineProtect=1 report machine_conns / machine_protect once per 5 s
// sample; the panel keeps only the latest sample per node in memory (no DB
// writes, no history). Absent = old agent = unknown, never 0.

// MachineConns is the agent's machine_conns: every row of /proc/net
// tcp/tcp6/udp/udp6 (all states), the number the mmwx server card shows.
type MachineConns struct {
	Total       int64 `json:"total"`
	TCP         int64 `json:"tcp"`
	UDP         int64 `json:"udp"`
	Established int64 `json:"established"`
	TimeWait    int64 `json:"time_wait"`
	CloseWait   int64 `json:"close_wait"`
	OtherTCP    int64 `json:"other_tcp"`
}

// MachineProtect is the agent's machine_protect.
type MachineProtect struct {
	Owner            string `json:"owner"`
	Enabled          bool   `json:"enabled"`
	MaxTotal         *int64 `json:"max_total,omitempty"`
	TWBuckets        *int64 `json:"tw_buckets,omitempty"`
	CloseWaitSeconds *int64 `json:"close_wait_seconds,omitempty"`
	Blocking         bool   `json:"blocking"`
	DroppedTotal     int64  `json:"dropped_total"`
	Ports            int    `json:"ports,omitempty"`
	Error            string `json:"error,omitempty"`
	MmwxStatus       string `json:"mmwx_status,omitempty"`
}

// MachineSample is the latest report of one node.
type MachineSample struct {
	Conns   *MachineConns
	Protect *MachineProtect
	At      time.Time
}

// machineFreshFor: an agent reports every 5 s; older samples (node offline or
// agent stuck) are unknown.
const machineFreshFor = 30 * time.Second

// machineRetainFor bounds the store: entries of nodes gone this long are dropped.
const machineRetainFor = 10 * time.Minute

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func positivePtr(v *int64) *int64 {
	if v == nil || *v <= 0 {
		return nil
	}
	out := *v
	return &out
}

// SanitizeMachine clamps reported values (agents and Custom are not trusted).
func SanitizeMachine(c *MachineConns, p *MachineProtect) (*MachineConns, *MachineProtect) {
	return sanitizeMachine(c, p)
}

func sanitizeMachine(c *MachineConns, p *MachineProtect) (*MachineConns, *MachineProtect) {
	var conns *MachineConns
	if c != nil {
		v := MachineConns{TCP: nonNegative(c.TCP), UDP: nonNegative(c.UDP), Established: nonNegative(c.Established),
			TimeWait: nonNegative(c.TimeWait), CloseWait: nonNegative(c.CloseWait), OtherTCP: nonNegative(c.OtherTCP)}
		v.Total = v.TCP + v.UDP
		conns = &v
	}
	var prot *MachineProtect
	if p != nil && (p.Owner == "flvx" || p.Owner == "mmwx") {
		v := MachineProtect{Owner: p.Owner, Enabled: p.Enabled, MaxTotal: positivePtr(p.MaxTotal), TWBuckets: positivePtr(p.TWBuckets),
			CloseWaitSeconds: positivePtr(p.CloseWaitSeconds), Blocking: p.Blocking, DroppedTotal: nonNegative(p.DroppedTotal)}
		if p.Ports > 0 && p.Ports <= 131070 {
			v.Ports = p.Ports
		}
		if r := []rune(p.Error); len(r) > 300 {
			v.Error = string(r[:300])
		} else {
			v.Error = p.Error
		}
		switch p.MmwxStatus {
		case "ok", "stale", "missing":
			v.MmwxStatus = p.MmwxStatus
		}
		prot = &v
	}
	return conns, prot
}

// recordMachine stores the sample of one metric message (only messages that
// carry the fields; the 1 s metrics in between keep the last sample).
func (s *Server) recordMachine(nodeID int64, info SystemInfo, now time.Time) {
	if info.MachineConns == nil && info.MachineProtect == nil {
		return
	}
	conns, prot := sanitizeMachine(info.MachineConns, info.MachineProtect)
	s.machineMu.Lock()
	s.machineStats[nodeID] = &MachineSample{Conns: conns, Protect: prot, At: now}
	s.machineMu.Unlock()
}

// MachineStatus returns a copy of the node's latest fresh sample, or nil.
func (s *Server) MachineStatus(nodeID int64) *MachineSample {
	if s == nil {
		return nil
	}
	s.machineMu.RLock()
	sample := s.machineStats[nodeID]
	s.machineMu.RUnlock()
	if sample == nil || time.Since(sample.At) > machineFreshFor {
		return nil
	}
	out := *sample
	return &out
}

func (s *Server) pruneMachineStats(now time.Time) {
	s.machineMu.Lock()
	for id, sample := range s.machineStats {
		if now.Sub(sample.At) > machineRetainFor {
			delete(s.machineStats, id)
		}
	}
	s.machineMu.Unlock()
}

// ForgetMachine drops a deleted node's sample at once.
func (s *Server) ForgetMachine(nodeID int64) {
	if s == nil {
		return
	}
	s.machineMu.Lock()
	delete(s.machineStats, nodeID)
	s.machineMu.Unlock()
}

// MachineConnsView / MachineProtectView / MachineView are the API shapes.
type MachineConnsView struct {
	Total       int64 `json:"total"`
	TCP         int64 `json:"tcp"`
	UDP         int64 `json:"udp"`
	Established int64 `json:"established"`
	TimeWait    int64 `json:"timeWait"`
	CloseWait   int64 `json:"closeWait"`
	OtherTCP    int64 `json:"otherTcp"`
}

type MachineProtectView struct {
	Owner            string `json:"owner"`
	Enabled          bool   `json:"enabled"`
	MaxTotal         *int64 `json:"maxTotal"`
	TWBuckets        *int64 `json:"twBuckets,omitempty"`
	CloseWaitSeconds *int64 `json:"closeWaitSeconds,omitempty"`
	Blocking         bool   `json:"blocking"`
	DroppedTotal     int64  `json:"droppedTotal"`
	Ports            int    `json:"ports,omitempty"`
	Error            string `json:"error,omitempty"`
	MmwxStatus       string `json:"mmwxStatus,omitempty"`
}

type MachineView struct {
	Conns     *MachineConnsView   `json:"conns,omitempty"`
	Protect   *MachineProtectView `json:"protect,omitempty"`
	UpdatedAt int64               `json:"updatedAt"`
}

// View renders a sample for the API. Non-admins (a monitor user who already sees
// the node) get only what the chip shows: counts, the cap, blocking, dropped
// count and owner — no settings and no error text.
func (m *MachineSample) View(admin bool) *MachineView {
	if m == nil {
		return nil
	}
	v := &MachineView{UpdatedAt: m.At.UnixMilli()}
	if c := m.Conns; c != nil {
		v.Conns = &MachineConnsView{Total: c.Total, TCP: c.TCP, UDP: c.UDP, Established: c.Established, TimeWait: c.TimeWait, CloseWait: c.CloseWait, OtherTCP: c.OtherTCP}
	}
	if p := m.Protect; p != nil {
		pv := &MachineProtectView{Owner: p.Owner, Enabled: p.Enabled, Blocking: p.Blocking, DroppedTotal: p.DroppedTotal}
		if p.Enabled {
			pv.MaxTotal = positivePtr(p.MaxTotal)
		}
		if admin {
			pv.MaxTotal = positivePtr(p.MaxTotal)
			pv.TWBuckets = positivePtr(p.TWBuckets)
			pv.CloseWaitSeconds = positivePtr(p.CloseWaitSeconds)
			pv.Ports, pv.Error, pv.MmwxStatus = p.Ports, p.Error, p.MmwxStatus
		}
		v.Protect = pv
	}
	return v
}

// SyncMachineProtection pushes the node's setting through the command channel.
// The handshake capability gates it, so older agents never get an unknown command.
func (s *Server) SyncMachineProtection(nodeID int64, m repo.MachineProtection) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	ns := s.nodes[nodeID]
	capable := ns != nil && ns.machineProtect >= 1
	s.mu.RUnlock()
	if !capable {
		return nil
	}
	result, err := s.SendCommand(nodeID, "SetMachineProtection", map[string]interface{}{
		"enabled": m.Enabled, "maxTotal": m.MaxTotal, "twBuckets": m.TWBuckets, "closeWaitSeconds": m.CloseWaitSeconds,
	}, 5*time.Second)
	if err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("%s", result.Message)
	}
	return nil
}

// MachineProtectCapable reports whether the node's agent applies the setting.
func (s *Server) MachineProtectCapable(nodeID int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := s.nodes[nodeID]
	return ns != nil && ns.machineProtect >= 1
}

// metricBroadcastVariants returns the metric data for admin browser sessions and
// for everybody else (non-admin users, public status page). Both drop the
// panel-only fields as before; the machine fields go to admins only, so other
// sessions learn nothing new about nodes they cannot see.
func metricBroadcastVariants(raw []byte) (admin, other string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return string(raw), string(raw)
	}
	_, quota := fields["quotaGroups"]
	_, raStatus := fields["ipv6_ra_status"]
	_, raDetail := fields["ipv6_ra_detail"]
	_, leikwan := fields["leikwan"]
	_, conns := fields["machine_conns"]
	_, prot := fields["machine_protect"]
	if !quota && !raStatus && !raDetail && !leikwan && !conns && !prot {
		return string(raw), string(raw)
	}
	delete(fields, "quotaGroups")
	// RA details belong to the admin node list, never public/non-admin metrics.
	delete(fields, "ipv6_ra_status")
	delete(fields, "ipv6_ra_detail")
	delete(fields, "leikwan")
	adminJSON, err := json.Marshal(fields)
	if err != nil {
		return string(raw), string(raw)
	}
	if !conns && !prot {
		return string(adminJSON), string(adminJSON)
	}
	delete(fields, "machine_conns")
	delete(fields, "machine_protect")
	otherJSON, err := json.Marshal(fields)
	if err != nil {
		return string(adminJSON), string(adminJSON)
	}
	return string(adminJSON), string(otherJSON)
}
