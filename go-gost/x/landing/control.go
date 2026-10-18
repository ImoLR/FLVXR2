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
	"strconv"
	"strings"
	"time"
)

type coreSnapshot struct {
	Version   int       `json:"version"`
	StartedAt time.Time `json:"started_at"`
	Inbounds  []struct {
		InboundTag string `json:"inbound_tag"`
	} `json:"inbounds"`
	Users []struct {
		Identity struct {
			InboundTag string `json:"inbound_tag"`
		} `json:"identity"`
		CurrentTotal int64 `json:"current_total"`
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

func (m *Manager) applyLimits(ctx context.Context) error {
	// Helper connectionSettings.coreConfig maps the enabled global total limit
	// and ports to these v7 control-socket fields. nil means unlimited.
	var global *int64
	if m.desired.MaxConnections > 0 {
		v := m.desired.MaxConnections
		global = &v
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
	cfg := map[string]any{"max_global_total_connections": global, "max_global_inbound_connections": nil, "online_ip_grace_period_seconds": 0, "default_close_wait_timeout_seconds": nil, "port_limits": ports, "limits": []any{}, "management_mappings": []any{}, "management_limits": []any{}, "blocked_identities": []any{}}
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
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := []string{"api", command, "--server=127.0.0.1:" + strconv.Itoa(m.apiPort), "--timeout=3"}
	argv = append(argv, args...)
	output, err := exec.CommandContext(child, m.binary(), argv...).Output()
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
			return nil
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
	if err := m.applyLimits(ctx); err != nil {
		return err
	}
	wanted := map[string]string{}
	for _, n := range m.desired.Nodes {
		if m.desired.Enabled && n.Enabled {
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
	// Preserve all counters before disabling an inbound.
	_ = m.collect(ctx)
	for tag := range present {
		if !strings.HasPrefix(tag, "flvxl-") {
			continue
		}
		want, exists := wanted[tag]
		if !exists || m.applied.Inbounds[tag] != want {
			if _, err := m.api(ctx, "rmi", tag); err != nil {
				return err
			}
			delete(present, tag)
			delete(m.applied.Inbounds, tag)
			if err := m.writeJSON("applied.json", m.applied); err != nil {
				return err
			}
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
	return m.collect(ctx)
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
	for _, u := range snapshot.Users {
		if c := byTag[u.Identity.InboundTag]; c != nil {
			c.Connections += u.CurrentTotal
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
