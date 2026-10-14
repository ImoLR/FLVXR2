package socket

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/go-gost/x/registry"
)

type serviceInventory struct {
	Services   []string `json:"services"`
	Chains     []string `json:"chains"`
	Hops       []string `json:"hops"`
	Limiters   []string `json:"limiters"`
	Nftables   []string `json:"nftables"`
	WGPaths    []string `json:"wgPaths"`
	WGForwards []string `json:"wgForwards"`
}

func inventoryNames[T any](items map[string]T) []string {
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// List only names, never credentials/configuration. This command does not
// sample counters, save configuration, reconcile rules or modify the registry.
func (w *WebSocketReporter) listServices() (serviceInventory, error) {
	i := serviceInventory{
		Services: inventoryNames(registry.ServiceRegistry().GetAll()),
		Chains:   inventoryNames(registry.ChainRegistry().GetAll()),
		Hops:     inventoryNames(registry.HopRegistry().GetAll()),
		Limiters: inventoryNames(registry.TrafficLimiterRegistry().GetAll()),
		Nftables: []string{}, WGPaths: []string{}, WGForwards: []string{},
	}
	if w.nftablesMgr != nil {
		for _, id := range w.nftablesMgr.ListForwardIDs() {
			i.Nftables = append(i.Nftables, fmt.Sprintf("%d_nft", id))
		}
		sort.Strings(i.Nftables)
	}
	paths := map[string]bool{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return i, err
	}
	for _, iface := range interfaces {
		if strings.HasPrefix(iface.Name, "wg-flvx-") {
			paths[iface.Name] = true
		}
	}
	for _, pattern := range []struct{ dir, prefix, suffix string }{
		{"/etc/flvx_agent/wireguard", "path-", ".conf"}, {"/etc/flvx_agent/wg", "forward-", ".json"},
	} {
		entries, err := os.ReadDir(pattern.dir)
		if err != nil && !os.IsNotExist(err) {
			return i, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasPrefix(name, pattern.prefix) || !strings.HasSuffix(name, pattern.suffix) {
				continue
			}
			raw := strings.TrimSuffix(strings.TrimPrefix(name, pattern.prefix), pattern.suffix)
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
				continue
			}
			if pattern.prefix == "path-" {
				paths["wg-flvx-"+raw] = true
			} else {
				i.WGForwards = append(i.WGForwards, "forward-"+raw)
			}
		}
	}
	i.WGPaths = inventoryNames(paths)
	sort.Strings(i.WGForwards)
	return i, nil
}
