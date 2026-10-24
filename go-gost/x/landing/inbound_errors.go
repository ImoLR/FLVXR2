package landing

import (
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Plan 147: a landing inbound the Core cannot add (typically: another program
// on the machine already listens on its port) no longer stops the reconcile —
// the other inbounds of the server are still added — and is reported per node
// with its port and a short reason, so the panel can show it (and move a
// never-used node to another port). The failed add is retried with a bounded
// backoff instead of on every 10 s tick, and logged once (logsafe routine log:
// first failure, a 10-minute count, one recovery line).

// NodeError is one node whose inbound is not running on this agent.
type NodeError struct {
	ID   int64 `json:"id"`
	Port int   `json:"port"`
	// Code: "port_in_use" (another program holds the port; Reason says TCP or
	// UDP) or "add_failed" (the Core refused the inbound for another reason).
	Code   string `json:"code"`
	Reason string `json:"reason"`
	// Since is the first failure of this configuration (unix ms).
	Since int64 `json:"since"`
}

const (
	NodeErrorPortInUse = "port_in_use"
	NodeErrorAddFailed = "add_failed"
)

// inboundRetryDelays: 1st retry on the next tick, then slower; capped at 10 min.
var inboundRetryDelays = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

type inboundFailure struct {
	cfg      string
	revision int64
	failures int
	next     time.Time
	err      NodeError
}

// inboundRetryDue: a tag whose last add failed with the same configuration in
// the same desired revision waits for its backoff; a changed configuration
// (e.g. the panel moved the port) or a new revision retries at once.
func (m *Manager) inboundRetryDue(tag, cfg string, now time.Time) bool {
	f := m.inboundFails[tag]
	if f == nil || f.cfg != cfg || f.revision != m.desired.Revision {
		return true
	}
	return !now.Before(f.next)
}

func (m *Manager) noteInboundFailure(tag, cfg string, n Node, err error, now time.Time) {
	if m.inboundFails == nil {
		m.inboundFails = map[string]*inboundFailure{}
	}
	f := m.inboundFails[tag]
	if f == nil || f.cfg != cfg {
		f = &inboundFailure{cfg: cfg}
		f.err.Since = now.UnixMilli()
		m.inboundFails[tag] = f
	}
	f.revision = m.desired.Revision
	delay := inboundRetryDelays[min(f.failures, len(inboundRetryDelays)-1)]
	f.failures++
	f.next = now.Add(delay)
	code, reason := classifyInboundFailure(n, err)
	f.err.ID, f.err.Port, f.err.Code, f.err.Reason = n.ID, n.Port, code, reason
	m.logger().Record("landing inbound", tag, "port "+strconv.Itoa(n.Port)+": "+reason)
}

func (m *Manager) clearInboundFailure(tag string) {
	if _, ok := m.inboundFails[tag]; ok {
		delete(m.inboundFails, tag)
		m.logger().Record("landing inbound", tag, "")
	}
}

// pruneInboundFailures drops failures of tags no longer wanted (node removed,
// disabled or withheld): they are not an error any more.
func (m *Manager) pruneInboundFailures(wanted map[string]string) {
	for tag := range m.inboundFails {
		if _, ok := wanted[tag]; !ok {
			m.clearInboundFailure(tag)
		}
	}
}

// nodeErrors lists the current per-node failures (sorted by id); never nil,
// so the report always carries "node_errors" — its presence tells the panel
// this agent reports them.
func (m *Manager) nodeErrors() []NodeError {
	out := make([]NodeError, 0, len(m.inboundFails))
	for _, f := range m.inboundFails {
		out = append(out, f.err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// classifyInboundFailure probes the port the Core could not open (only after
// a failed add, so steady state costs nothing): if the agent cannot bind it
// either, another program holds it. The listen address and network are the
// inbound's own (0.0.0.0 = every address; SS2022 also uses UDP).
var probeListen = func(network, addr string) error {
	if strings.HasPrefix(network, "udp") {
		c, err := net.ListenPacket(network, addr)
		if err == nil {
			_ = c.Close()
		}
		return err
	}
	l, err := net.Listen(network, addr)
	if err == nil {
		_ = l.Close()
	}
	return err
}

func classifyInboundFailure(n Node, addErr error) (string, string) {
	listen := "0.0.0.0"
	if n.Listen != "" {
		listen = n.Listen
	}
	addr := net.JoinHostPort(listen, strconv.Itoa(n.Port))
	networks := []string{"tcp"}
	if n.Protocol == "ss2022" {
		networks = append(networks, "udp")
	}
	for _, network := range networks {
		err := probeListen(network, addr)
		if err == nil {
			continue
		}
		label := strings.ToUpper(network)
		switch {
		case errors.Is(err, syscall.EADDRINUSE):
			return NodeErrorPortInUse, label + ": address already in use"
		case errors.Is(err, syscall.EACCES):
			return NodeErrorAddFailed, label + ": permission denied"
		case errors.Is(err, syscall.EADDRNOTAVAIL):
			return NodeErrorAddFailed, label + ": cannot assign requested address"
		}
	}
	reason := "core rejected the inbound"
	if addErr != nil && strings.Contains(addErr.Error(), "address already in use") {
		return NodeErrorPortInUse, "address already in use"
	}
	return NodeErrorAddFailed, reason
}
