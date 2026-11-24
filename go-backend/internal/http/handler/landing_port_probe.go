package handler

import (
	"log"
	"time"
)

// Plan 304: a native landing port must not be handed out while another
// program on the machine listens on it (e.g. mmwx's xray on 127.0.0.1:38889
// inside the 30000–39999 range on Boil HKT 99u / Fachost seednet 黑五).
// Right before a pick (node create, the forward form's 新建落地, the plan-147
// move of a fresh node) the panel checks the next candidate port(s) with the
// TcpPing every agent already has: one TCP connect to 127.0.0.1:<port>, RST
// close. A connect that succeeds = a listener on loopback or on every
// address → the port is remembered in native_busy_ports (skipped by the
// picker; 24 h, probed again when it comes up again) and the next one is
// checked. Bounded (need + 3 connects, normally 1), only at allocation, never
// inside a DB transaction or the landing sync lock, and never blocking: an
// agent that is offline, slow or gives no usable answer leaves the pick as it
// was (plan 147 then reports a busy port and moves a fresh node).
const (
	landingPortProbeExtra     = 3 // connects beyond the ports needed
	landingPortProbeTimeout   = 2 * time.Second
	landingPortProbeBudget    = 5 * time.Second
	landingPortProbeConnectMS = 1000
)

// probeNativeLandingPorts checks the next candidate ports of a native server
// until need of them are free on the machine and returns the ones found
// taken (already remembered).
func (h *Handler) probeNativeLandingPorts(serverID int64, need int) []int {
	if h == nil || h.repo == nil || need <= 0 {
		return nil
	}
	probe, err := h.repo.NativeLandingPortCandidates(serverID, need+landingPortProbeExtra)
	if err != nil {
		log.Printf("landing port probe server=%d skipped: %v", serverID, err)
		return nil
	}
	if probe == nil {
		return nil
	}
	deadline := time.Now().Add(landingPortProbeBudget)
	var busy []int
	free := 0
	for _, port := range probe.Ports {
		if free >= need || time.Now().After(deadline) {
			break
		}
		taken, ok := h.landingPortTaken(probe.NodeID, port)
		if !ok {
			break // no usable answer: pick as before
		}
		if taken {
			busy = append(busy, port)
			continue
		}
		free++
	}
	if len(busy) == 0 {
		return nil
	}
	if err := h.repo.NoteNativeBusyPorts(serverID, busy, time.Now()); err != nil {
		log.Printf("landing port probe server=%d: busy ports %v not recorded: %v", serverID, busy, err)
		return nil
	}
	log.Printf("landing server=%d: ports %v are in use by another program on the machine; the picker skips them", serverID, busy)
	return busy
}

// landingPortTaken: one loopback TCP connect from the node's agent. ok is
// false when the agent gave no usable answer (offline, timeout, error).
func (h *Handler) landingPortTaken(nodeID int64, port int) (taken bool, ok bool) {
	res, err := h.sendNodeCommandWithTimeout(nodeID, "TcpPing", map[string]interface{}{
		"ip":      "127.0.0.1",
		"port":    port,
		"count":   1,
		"timeout": landingPortProbeConnectMS,
	}, landingPortProbeTimeout, false, false)
	if err != nil || res.Data == nil {
		return false, false
	}
	return asBool(res.Data["success"], false), true
}

// probeLandingCreate runs the probe for one node about to be created by uid
// (admins: any server; users: only a server they may use, so a refused
// create costs the machine nothing).
func (h *Handler) probeLandingCreate(uid, serverID int64, admin bool) {
	if serverID <= 0 {
		return
	}
	if !admin {
		if ok, err := h.repo.UserCanUseLandingServer(uid, serverID); err != nil || !ok {
			return
		}
	}
	h.probeNativeLandingPorts(serverID, 1)
}
