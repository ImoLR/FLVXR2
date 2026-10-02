package socket

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/go-gost/x/registry"
)

func decodeCommandData(data interface{}, target interface{}) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

const maxServiceClientIPs = 500

type clientIPCount struct {
	IP          string `json:"ip"`
	Connections int    `json:"connections"`
}

type serviceClientIPSnapshot struct {
	IPs             []clientIPCount `json:"ips"`
	IPCount         int             `json:"ipCount"`
	ConnectionCount int             `json:"connectionCount"`
	Truncated       bool            `json:"truncated"`
}

func snapshotClientIPs(counts map[string]int) serviceClientIPSnapshot {
	ips := make([]string, 0, len(counts))
	snapshot := serviceClientIPSnapshot{IPs: make([]clientIPCount, 0)}
	for ip, count := range counts {
		if count > 0 {
			ips = append(ips, ip)
			snapshot.ConnectionCount += count
		}
	}
	sort.Strings(ips)
	snapshot.IPCount = len(ips)
	snapshot.Truncated = len(ips) > maxServiceClientIPs
	if len(ips) > maxServiceClientIPs {
		ips = ips[:maxServiceClientIPs]
	}
	for _, ip := range ips {
		snapshot.IPs = append(snapshot.IPs, clientIPCount{IP: ip, Connections: counts[ip]})
	}
	return snapshot
}

func (w *WebSocketReporter) handleGetServiceClientIPs(data interface{}) (map[string]interface{}, error) {
	var req struct {
		ForwardIDs []int64 `json:"forwardIds"`
	}
	if err := decodeCommandData(data, &req); err != nil {
		return nil, err
	}
	if len(req.ForwardIDs) == 0 || len(req.ForwardIDs) > 100 {
		return nil, fmt.Errorf("forwardIds must contain 1 to 100 IDs")
	}
	ids := make(map[int64]struct{}, len(req.ForwardIDs))
	for _, id := range req.ForwardIDs {
		if id <= 0 {
			return nil, fmt.Errorf("invalid forward ID %d", id)
		}
		ids[id] = struct{}{}
	}
	services := make(map[string]serviceClientIPSnapshot)
	for name, svc := range registry.ServiceRegistry().GetAll() {
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			continue
		}
		if _, ok := ids[id]; !ok {
			continue
		}
		if source, ok := svc.(interface{ ClientIPCounts() map[string]int }); ok {
			services[name] = snapshotClientIPs(source.ClientIPCounts())
		}
	}
	if w.nftablesMgr != nil {
		nftIDs := make([]int64, 0, len(ids))
		for id := range ids {
			nftIDs = append(nftIDs, id)
		}
		nftIPs, err := w.nftablesMgr.GetForwardClientIPs(nftIDs)
		if err != nil {
			return nil, err
		}
		for id, counts := range nftIPs {
			services[fmt.Sprintf("%d_nft", id)] = snapshotClientIPs(counts)
		}
	}
	return map[string]interface{}{"services": services}, nil
}
