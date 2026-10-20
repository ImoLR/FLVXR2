package handler

import (
	"encoding/json"
	"errors"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Machine connection protection (plan 118): node settings, push to agents and
// the in-memory whole-machine connection samples in API responses.

var machineProtectionKeys = []string{"machineProtectEnabled", "machineMaxTotal", "machineTwBuckets", "machineCloseWaitSeconds"}

func machineInt(v interface{}, label string) (int64, error) {
	switch x := v.(type) {
	case float64:
		if x < 0 || x != math.Trunc(x) || x > math.MaxInt32 {
			return 0, errors.New(label + "须为非负整数")
		}
		return int64(x), nil
	case json.Number:
		n, err := x.Int64()
		if err != nil || n < 0 {
			return 0, errors.New(label + "须为非负整数")
		}
		return n, nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil || n < 0 {
			return 0, errors.New(label + "须为非负整数")
		}
		return n, nil
	case int:
		if x < 0 {
			return 0, errors.New(label + "须为非负整数")
		}
		return int64(x), nil
	case int64:
		if x < 0 {
			return 0, errors.New(label + "须为非负整数")
		}
		return x, nil
	}
	return 0, errors.New(label + "须为非负整数")
}

// parseMachineProtection overlays the request's machine* keys on base. present
// reports whether the request carried any of them.
func parseMachineProtection(req map[string]interface{}, base repo.MachineProtection) (repo.MachineProtection, bool, error) {
	present := false
	for _, key := range machineProtectionKeys {
		if _, ok := req[key]; ok {
			present = true
		}
	}
	if !present {
		return base, false, nil
	}
	out := base
	if v, ok := req["machineProtectEnabled"]; ok {
		switch b := v.(type) {
		case bool:
			out.Enabled = b
		case string:
			switch strings.ToLower(strings.TrimSpace(b)) {
			case "true", "1":
				out.Enabled = true
			case "false", "0":
				out.Enabled = false
			default:
				return base, true, errors.New("连接保护开关无效")
			}
		default:
			return base, true, errors.New("连接保护开关无效")
		}
	}
	var err error
	if v, ok := req["machineMaxTotal"]; ok {
		if out.MaxTotal, err = machineInt(v, "整机连接上限"); err != nil {
			return base, true, err
		}
	}
	if v, ok := req["machineTwBuckets"]; ok {
		if out.TWBuckets, err = machineInt(v, "TIME_WAIT 上限"); err != nil {
			return base, true, err
		}
	}
	if v, ok := req["machineCloseWaitSeconds"]; ok {
		if out.CloseWaitSeconds, err = machineInt(v, "CLOSE_WAIT 超时"); err != nil {
			return base, true, err
		}
	}
	if err := out.Validate(); err != nil {
		return base, true, err
	}
	return out, true, nil
}

// syncNodeMachineProtection pushes the stored setting to a capable online agent.
// A failed push is retried on the next reconnect (the agent keeps its last one).
func (h *Handler) syncNodeMachineProtection(nodeID int64) {
	if h == nil || h.wsServer == nil || h.repo == nil || !h.wsServer.MachineProtectCapable(nodeID) {
		return
	}
	m, err := h.repo.GetNodeMachineProtection(nodeID)
	if err != nil {
		log.Printf("node %d machine protection load failed: %v", nodeID, err)
		return
	}
	if err := h.wsServer.SyncMachineProtection(nodeID, m); err != nil {
		log.Printf("node %d machine protection push failed (retried on reconnect): %v", nodeID, err)
	}
}

// machineView is the node's latest whole-machine sample for an API response
// (nil = unknown: offline, old agent or no sample yet).
func (h *Handler) machineView(nodeID int64, admin bool) *ws.MachineView {
	if h == nil || h.wsServer == nil {
		return nil
	}
	return h.wsServer.MachineStatus(nodeID).View(admin)
}

// Shared landing servers report their machine state through Custom's FLVX status
// (polled once a minute per server). Samples older than this are unknown.
const sharedMachineFreshFor = 3 * time.Minute

type sharedMachineSample struct {
	sample ws.MachineSample
}

func (h *Handler) recordSharedMachine(serverID int64, m *landingcustom.Machine, now time.Time) {
	h.sharedMachineMu.Lock()
	defer h.sharedMachineMu.Unlock()
	if h.sharedMachine == nil {
		h.sharedMachine = map[int64]sharedMachineSample{}
	}
	for id, s := range h.sharedMachine {
		if now.Sub(s.sample.At) > 10*time.Minute {
			delete(h.sharedMachine, id) // bounded: removed/paused shares age out
		}
	}
	if m == nil || (m.Conns == nil && m.Protect == nil) {
		delete(h.sharedMachine, serverID)
		return
	}
	// The Helper's own sample time when given (a Helper that stopped updating
	// shows as unknown after sharedMachineFreshFor), never a future time.
	at := now
	if t, err := time.Parse(time.RFC3339Nano, m.UpdatedAt); err == nil && t.Before(now) {
		at = t
	}
	var conns *ws.MachineConns
	if c := m.Conns; c != nil {
		conns = &ws.MachineConns{Total: c.Total, TCP: c.TCP, UDP: c.UDP, Established: c.Established, TimeWait: c.TimeWait, CloseWait: c.CloseWait, OtherTCP: c.OtherTCP}
	}
	var prot *ws.MachineProtect
	if p := m.Protect; p != nil {
		owner := p.Owner
		if owner == "" {
			owner = "mmwx"
		}
		prot = &ws.MachineProtect{Owner: owner, Enabled: p.Enabled, MaxTotal: p.MaxTotal, TWBuckets: p.TWBuckets, CloseWaitSeconds: p.CloseWaitSeconds, Blocking: p.Blocking, DroppedTotal: p.DroppedTotal}
	}
	c, pr := ws.SanitizeMachine(conns, prot)
	h.sharedMachine[serverID] = sharedMachineSample{sample: ws.MachineSample{Conns: c, Protect: pr, At: at}}
}

func (h *Handler) sharedMachineView(serverID int64, admin bool, now time.Time) *ws.MachineView {
	h.sharedMachineMu.Lock()
	s, ok := h.sharedMachine[serverID]
	h.sharedMachineMu.Unlock()
	if !ok || now.Sub(s.sample.At) > sharedMachineFreshFor {
		return nil
	}
	sample := s.sample
	return sample.View(admin)
}

func (h *Handler) forgetSharedMachine(serverID int64) {
	h.sharedMachineMu.Lock()
	delete(h.sharedMachine, serverID)
	h.sharedMachineMu.Unlock()
}
