package ws

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/clientip"
)

// Node session close logging (plan 138): one line per ended agent session
// with node id, lifetime, why it ended and which path it used. Bounded: at
// most one line per node per 10 s (skipped ones are counted into the next).

const nodeSessionLogEvery = 10 * time.Second

// nodeSessionVia describes how an agent session reached the panel: through
// Cloudflare (edge colo from Cf-Ray, client address) or directly.
func nodeSessionVia(r *http.Request) string {
	if r == nil {
		return ""
	}
	client := clientip.FromRequest(r)
	family := "ipv4"
	if ip := net.ParseIP(client); ip != nil && ip.To4() == nil {
		family = "ipv6"
	}
	if ray := strings.TrimSpace(r.Header.Get("Cf-Ray")); ray != "" {
		colo := "?"
		if i := strings.LastIndex(ray, "-"); i >= 0 && i+1 < len(ray) {
			colo = ray[i+1:]
		}
		return fmt.Sprintf("cloudflare colo=%s client=%s %s", colo, client, family)
	}
	return fmt.Sprintf("direct client=%s %s", client, family)
}

// classifyNodeSessionEnd says why a node session's read loop ended.
func classifyNodeSessionEnd(err error, replaced bool, keepalive string) string {
	if replaced {
		return "replaced by a newer connection of the same node"
	}
	if keepalive != "" {
		return "keepalive ping write failed: " + keepalive
	}
	var ce *websocket.CloseError
	var ne net.Error
	switch {
	case err == nil:
		return "ended"
	case errors.As(err, &ce) && ce.Code == websocket.CloseAbnormalClosure:
		return "connection closed without close frame (EOF)"
	case errors.As(err, &ce):
		text := strings.TrimSpace(ce.Text)
		if text != "" {
			return fmt.Sprintf("close frame code=%d %q", ce.Code, text)
		}
		return fmt.Sprintf("close frame code=%d", ce.Code)
	case errors.As(err, &ne) && ne.Timeout():
		return fmt.Sprintf("no pong/data for %s (pong timeout)", wsPongWait)
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "EOF"
	case errors.Is(err, net.ErrClosed):
		return "closed by the panel"
	}
	msg := err.Error()
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	return "read error: " + msg
}

type nodeSessionLogState struct {
	last       time.Time
	suppressed int
	ends       []time.Time // session ends in the last hour (bounded)
}

type nodeSessionLogger struct {
	mu    sync.Mutex
	nodes map[int64]*nodeSessionLogState
}

var nodeSessionLogPrint = func(line string) { log.Print(line) }

// record notes one ended session and returns the line to print ("" when rate
// limited). The per-node state is tiny and pruned to the last hour.
func (l *nodeSessionLogger) record(nodeID int64, now time.Time, line func(endsLastHour, skipped int) string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nodes == nil {
		l.nodes = make(map[int64]*nodeSessionLogState)
	}
	st := l.nodes[nodeID]
	if st == nil {
		st = &nodeSessionLogState{}
		l.nodes[nodeID] = st
	}
	keep := st.ends[:0]
	for _, t := range st.ends {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	st.ends = keep
	if len(st.ends) < 240 {
		st.ends = append(st.ends, now)
	}
	if !st.last.IsZero() && now.Sub(st.last) < nodeSessionLogEvery {
		st.suppressed++
		return ""
	}
	skipped := st.suppressed
	st.last, st.suppressed = now, 0
	return line(len(st.ends), skipped)
}

// EndedSessionsLastHour returns how many sessions of a node ended in the last
// hour (memory only; 0 after a panel restart).
func (s *Server) EndedSessionsLastHour(nodeID int64) int {
	s.sessionLog.mu.Lock()
	defer s.sessionLog.mu.Unlock()
	st := s.sessionLog.nodes[nodeID]
	if st == nil {
		return 0
	}
	n := 0
	now := time.Now()
	for _, t := range st.ends {
		if now.Sub(t) < time.Hour {
			n++
		}
	}
	return n
}

func (s *Server) logNodeSessionEnd(ns *nodeSession, err error) {
	if ns == nil {
		return
	}
	now := time.Now()
	replaced := ns.replacedAt.Load() > 0
	keepalive := ""
	if p := ns.conn.keepaliveErr.Load(); p != nil {
		keepalive = *p
	}
	reason := classifyNodeSessionEnd(err, replaced, keepalive)
	line := s.sessionLog.record(ns.nodeID, now, func(endsLastHour, skipped int) string {
		var b strings.Builder
		fmt.Fprintf(&b, "[ws] node %d session ended after %s: %s", ns.nodeID, now.Sub(ns.startedAt).Round(100*time.Millisecond), reason)
		if last := ns.lastMsgAt.Load(); last > 0 {
			ref := now
			if replaced {
				ref = time.Unix(0, ns.replacedAt.Load())
			}
			gap := ref.Sub(time.Unix(0, last))
			if gap < 0 {
				gap = 0
			}
			if replaced {
				fmt.Fprintf(&b, " (old connection silent %.1fs before the new one arrived)", gap.Seconds())
			} else {
				fmt.Fprintf(&b, " (last message %.1fs before)", gap.Seconds())
			}
		}
		if ns.via != "" {
			fmt.Fprintf(&b, "; via %s", ns.via)
		}
		fmt.Fprintf(&b, "; ended sessions of this node in the last hour: %d", endsLastHour)
		if skipped > 0 {
			fmt.Fprintf(&b, " (%d more ends in the previous 10 s not logged)", skipped)
		}
		return b.String()
	})
	if line != "" {
		nodeSessionLogPrint(line)
	}
}
