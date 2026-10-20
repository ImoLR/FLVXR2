package landing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type coreSnapshot struct {
	Version   int       `json:"version"`
	StartedAt time.Time `json:"started_at"`
	Inbounds  []struct {
		InboundTag    string     `json:"inbound_tag"`
		OnlineIPCount *int       `json:"online_ip_count"`
		OnlineIPs     []OnlineIP `json:"online_ips"`
	} `json:"inbounds"`
	Users []struct {
		Identity struct {
			InboundTag string `json:"inbound_tag"`
			User       string `json:"user"`
		} `json:"identity"`
		CurrentTotal     int64           `json:"current_total"`
		InboundOnlineIPs json.RawMessage `json:"inbound_online_ips"`
	} `json:"proxy_users"`
}

func (m *Manager) control(ctx context.Context, method, path string, body any, result any) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", m.socket())
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://landing-core"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("landing control unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("landing control returned HTTP %d", res.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(result); err != nil {
			return fmt.Errorf("invalid landing control response")
		}
	}
	return nil
}
func (m *Manager) snapshot(ctx context.Context) (coreSnapshot, error) {
	var s coreSnapshot
	err := m.control(ctx, http.MethodGet, "/v1/snapshot", nil, &s)
	if err == nil && (s.Version != 7 || s.StartedAt.IsZero()) {
		err = fmt.Errorf("unsupported landing control version or epoch")
	}
	return s, err
}

type portLimit struct {
	InboundTag          string `json:"inbound_tag"`
	MaxTotalConnections *int64 `json:"max_total_connections"`
	MaxInboundOnlineIPs *int   `json:"max_inbound_online_ips"`
}

func (m *Manager) applyLimits(ctx context.Context, snapshot coreSnapshot) error {
	// Helper connectionSettings.coreConfig maps the enabled global total limit
	// and ports to these v7 control-socket fields. nil means unlimited.
	var global *int64
	if m.desired.MaxConnections > 0 {
		v := m.desired.MaxConnections
		global = &v
	}
	// Removing an inbound only closes its listener. The pinned v7 control API
	// closes existing sockets for blocked identities, including pre-auth sockets.
	// Include removed tags while the previous applied state still identifies them.
	active := map[string]bool{}
	for _, n := range m.desired.Nodes {
		// Nodes withheld by a failed routing plan lose existing sockets too, so
		// nothing keeps leaving through the default outbound.
		active[n.InboundTag] = m.desired.Enabled && n.Enabled && !m.withheld[n.InboundTag]
	}
	blocked := []map[string]string{}
	seen := map[string]bool{}
	block := func(tag, user string) {
		key := tag + "\x00" + user
		if active[tag] || seen[key] || !strings.HasPrefix(tag, "flvxl-") {
			return
		}
		seen[key] = true
		blocked = append(blocked, map[string]string{"inbound_tag": tag, "user": user})
	}
	for _, n := range m.desired.Nodes {
		block(n.InboundTag, n.InboundTag)
		block(n.InboundTag, "")
	}
	for tag := range m.applied.Inbounds {
		block(tag, tag)
		block(tag, "")
	}
	for _, u := range snapshot.Users {
		block(u.Identity.InboundTag, u.Identity.User)
	}
	ports := []portLimit{}
	for _, n := range m.desired.Nodes {
		p := portLimit{InboundTag: n.InboundTag}
		if n.MaxConnections > 0 {
			v := n.MaxConnections
			p.MaxTotalConnections = &v
		}
		if n.MaxOnlineIPs > 0 {
			v := n.MaxOnlineIPs
			p.MaxInboundOnlineIPs = &v
		}
		ports = append(ports, p)
	}
	// Machine protection (plan 118): the Core closes an outbound TCP socket that
	// has been in CLOSE_WAIT (remote sent FIN, Core still open) for this long.
	var closeWait *int64
	if v := m.closeWait.Load(); v > 0 {
		closeWait = &v
	}
	cfg := map[string]any{"max_global_total_connections": global, "max_global_inbound_connections": nil, "online_ip_grace_period_seconds": 0, "default_close_wait_timeout_seconds": closeWait, "port_limits": ports, "limits": []any{}, "management_mappings": []any{}, "management_limits": []any{}, "blocked_identities": blocked}
	var result struct {
		Success bool `json:"success"`
	}
	if err := m.control(ctx, http.MethodPut, "/v1/config", cfg, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("landing Core rejected connection limits")
	}
	return nil
}

// The pinned Core's API CLI performs gRPC AddInbound/RemoveInbound/QueryStats;
// using it avoids importing a second copy of the Core and its protobuf graph.
func (m *Manager) api(ctx context.Context, command string, args ...string) ([]byte, error) {
	// Rule replacement builds geo matchers inside the CPU-capped Core.
	limit, rpc := 5*time.Second, "3"
	if command == "adrules" {
		limit, rpc = 30*time.Second, "25"
	}
	child, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	argv := []string{"api", command, "--server=127.0.0.1:" + strconv.Itoa(m.apiPort), "--timeout=" + rpc}
	argv = append(argv, args...)
	cmd := exec.CommandContext(child, m.command(), argv...)
	// The CLI (not the Core) reads geo files for rules it builds.
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+m.dir)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("landing gRPC %s failed", command)
	}
	return output, nil
}

func (m *Manager) reconcile(ctx context.Context) error {
	// A disabled never-installed server needs no Core download or daemon.
	if !m.desired.Enabled && m.apiPort == 0 {
		if _, err := os.Stat(filepath.Join(m.dir, "api.json")); os.IsNotExist(err) {
			return nil
		}
	}
	if err := m.setup(ctx); err != nil {
		return err
	}
	snapshot, err := m.snapshot(ctx)
	if err != nil {
		if !m.desired.Enabled {
			return fmt.Errorf("landing Core unavailable; final counters not confirmed")
		}
		if err := m.start(ctx); err != nil {
			return err
		}
		snapshot, err = m.snapshot(ctx)
		if err != nil {
			return err
		}
	}
	epoch := strconv.FormatInt(snapshot.StartedAt.UnixNano(), 10)
	if m.applied.Inbounds == nil {
		b, _ := os.ReadFile(filepath.Join(m.dir, "applied.json"))
		_ = json.Unmarshal(b, &m.applied)
	}
	if m.applied.Epoch != epoch {
		m.applied = appliedState{Epoch: epoch, Inbounds: map[string]string{}}
	}
	if m.applied.Inbounds == nil {
		m.applied.Inbounds = map[string]string{}
	}
	m.epoch = epoch
	// Capture the final live sample before controls terminate affected sockets.
	collectErr := m.collect(ctx)
	// Outbounds and rules go first: a fresh Core never serves an inbound before
	// its routing plan is in place. Egress failures are reported separately and
	// never mark the inbound state as failed.
	snapshot, _ = m.applyEgress(ctx, snapshot)
	m.withheld = m.withheldTags()
	m.egress.Withheld = nil
	for tag := range m.withheld {
		m.egress.Withheld = append(m.egress.Withheld, tag)
	}
	sort.Strings(m.egress.Withheld)
	if err := m.applyLimits(ctx, snapshot); err != nil {
		return err
	}
	wanted := map[string]string{}
	for _, n := range m.desired.Nodes {
		if m.desired.Enabled && n.Enabled && !m.withheld[n.InboundTag] {
			cfg, _ := n.inbound()
			wanted[n.InboundTag] = canonical(cfg)
		}
	}
	// ListInbounds is authoritative even after an agent crash between a gRPC call
	// and the applied-state rename. It includes idle inbounds absent from stats.
	raw, err := m.api(ctx, "lsi")
	if err != nil {
		return err
	}
	var listed struct {
		Inbounds []struct {
			Tag string `json:"tag"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return fmt.Errorf("invalid landing inbound list")
	}
	present := map[string]bool{}
	for _, in := range listed.Inbounds {
		present[in.Tag] = true
	}
	var remove []string
	for tag := range present {
		if !strings.HasPrefix(tag, "flvxl-") {
			continue
		}
		if want, exists := wanted[tag]; !exists || m.applied.Inbounds[tag] != want {
			remove = append(remove, tag)
		}
	}
	changed := len(remove) > 0
	for tag, cfg := range wanted {
		changed = changed || !present[tag] || m.applied.Inbounds[tag] != cfg
	}
	// Steady state (nothing to remove or add) needs no further samples: the one
	// taken before the controls is current and the flow upload samples again
	// every 5 s. Each sample runs the Core API CLI, so the 10 s tick stays at
	// one statsquery instead of three.
	if len(remove) > 0 {
		// Removing an inbound drops its counters: sample once more right before.
		// A failed sample must not prevent quota/expiry enforcement. Still reject
		// this Apply after removing affected inbounds so deletion retains its row.
		if err := m.collect(ctx); err != nil {
			collectErr = err
		}
	}
	for _, tag := range remove {
		if _, err := m.api(ctx, "rmi", tag); err != nil {
			return err
		}
		delete(present, tag)
		delete(m.applied.Inbounds, tag)
		if err := m.writeJSON("applied.json", m.applied); err != nil {
			return err
		}
	}
	for tag, cfg := range wanted {
		if present[tag] && m.applied.Inbounds[tag] == cfg {
			continue
		}
		path := filepath.Join(m.dir, "apply.json")
		if err := m.writeFile("apply.json", []byte(`{"inbounds":[`+cfg+`]}`), 0600); err != nil {
			return err
		}
		_, err := m.api(ctx, "adi", path)
		_ = os.Remove(path)
		if err != nil {
			return err
		}
		m.applied.Inbounds[tag] = cfg
		if err := m.writeJSON("applied.json", m.applied); err != nil {
			return err
		}
	}
	if changed {
		if err := m.collect(ctx); err != nil {
			return err
		}
	}
	return collectErr
}

func (m *Manager) collect(ctx context.Context) error {
	if m.apiPort == 0 {
		return nil
	}
	snapshot, err := m.snapshot(ctx)
	if err != nil {
		return err
	}
	epoch := strconv.FormatInt(snapshot.StartedAt.UnixNano(), 10)
	raw, err := m.api(ctx, "statsquery", "-pattern", "inbound>>>")
	if err != nil {
		return err
	}
	var result struct {
		Stat []struct {
			Name  string      `json:"name"`
			Value json.Number `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("invalid landing counter response")
	}
	byTag := map[string]*Counter{}
	for _, n := range m.desired.Nodes {
		byTag[n.InboundTag] = &Counter{ID: n.ID, InboundTag: n.InboundTag, Epoch: epoch}
	}
	for _, stat := range result.Stat {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[0] != "inbound" || parts[2] != "traffic" {
			continue
		}
		c := byTag[parts[1]]
		if c == nil {
			continue
		}
		if stat.Value == "" {
			stat.Value = "0"
		}
		v, err := stat.Value.Int64()
		if err != nil || v < 0 {
			return fmt.Errorf("invalid landing cumulative counter")
		}
		switch parts[3] {
		case "uplink":
			c.UpBytes = v
		case "downlink":
			c.DownBytes = v
		}
	}
	for _, in := range snapshot.Inbounds {
		if c := byTag[in.InboundTag]; c != nil && in.OnlineIPCount != nil {
			c.OnlineIPCount = in.OnlineIPCount
			c.OnlineIPs = in.OnlineIPs
		}
	}
	// The pinned Core exposes per-user IPs; newer snapshots can provide the
	// inbound aggregate directly. A present null list means no online IPs,
	// while an omitted field means this Core cannot report them.
	userIPs := map[string]map[string]int64{}
	unknownIPs := map[string]bool{}
	for _, u := range snapshot.Users {
		tag := u.Identity.InboundTag
		c := byTag[tag]
		if c == nil {
			continue
		}
		c.Connections += u.CurrentTotal
		if c.OnlineIPCount != nil {
			continue
		}
		if len(u.InboundOnlineIPs) == 0 {
			unknownIPs[tag] = true
			continue
		}
		var ips []OnlineIP
		if err := json.Unmarshal(u.InboundOnlineIPs, &ips); err != nil {
			return fmt.Errorf("invalid landing online IP response")
		}
		if userIPs[tag] == nil {
			userIPs[tag] = map[string]int64{}
		}
		for _, ip := range ips {
			userIPs[tag][ip.IP] += ip.Connections
		}
	}
	for tag, c := range byTag {
		if ips, ok := userIPs[tag]; c.OnlineIPCount == nil && ok && !unknownIPs[tag] {
			count := len(ips)
			c.OnlineIPCount = &count
			for ip, connections := range ips {
				c.OnlineIPs = append(c.OnlineIPs, OnlineIP{IP: ip, Connections: connections})
			}
		}
		sort.Slice(c.OnlineIPs, func(i, j int) bool {
			if c.OnlineIPs[i].Connections != c.OnlineIPs[j].Connections {
				return c.OnlineIPs[i].Connections > c.OnlineIPs[j].Connections
			}
			return c.OnlineIPs[i].IP < c.OnlineIPs[j].IP
		})
		if cap(c.OnlineIPs) > 64 {
			size := len(c.OnlineIPs)
			if size > 64 {
				size = 64
			}
			ips := make([]OnlineIP, size)
			copy(ips, c.OnlineIPs)
			c.OnlineIPs = ips
		}
	}
	// Removed/paused inbounds may have disappeared from StatsService. Retain the
	// maximum already observed value in this epoch until the panel acknowledges it.
	for _, old := range m.counters {
		if c := byTag[old.InboundTag]; c != nil && c.Epoch == old.Epoch {
			if old.UpBytes > c.UpBytes {
				c.UpBytes = old.UpBytes
			}
			if old.DownBytes > c.DownBytes {
				c.DownBytes = old.DownBytes
			}
		}
	}
	m.counters = make([]Counter, 0, len(byTag))
	for _, n := range m.desired.Nodes {
		m.counters = append(m.counters, *byTag[n.InboundTag])
	}
	m.epoch = epoch
	return nil
}
