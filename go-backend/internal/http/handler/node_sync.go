package handler

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type nodeSyncFailure struct {
	Type   string `json:"type"`
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type nodeSyncResult struct {
	TunnelsUpdated        int               `json:"tunnelsUpdated"`
	RulesUpdated          int               `json:"rulesUpdated"`
	EntryAddressesUpdated int               `json:"entryAddressesUpdated"`
	Failures              []nodeSyncFailure `json:"failures"`
}

func nodeAddressChanged(oldNode, newNode *model.Node) bool {
	return nodeDialAddressChanged(oldNode, newNode) || nodeExtraIPsChanged(oldNode, newNode)
}

// nodeDialAddressChanged covers the addresses other nodes dial. Extra IPs are
// excluded: they are only dialed as an explicit custom connect IP.
func nodeDialAddressChanged(oldNode, newNode *model.Node) bool {
	return strings.TrimSpace(oldNode.ServerIP) != strings.TrimSpace(newNode.ServerIP) ||
		strings.TrimSpace(oldNode.ServerIPV4.String) != strings.TrimSpace(newNode.ServerIPV4.String) ||
		strings.TrimSpace(oldNode.ServerIPV6.String) != strings.TrimSpace(newNode.ServerIPV6.String) ||
		strings.TrimSpace(oldNode.IntranetIP.String) != strings.TrimSpace(newNode.IntranetIP.String)
}

func nodeExtraIPsChanged(oldNode, newNode *model.Node) bool {
	return strings.Join(repo.ParseNodeExtraIPs(oldNode.ExtraIPs.String), ",") !=
		strings.Join(repo.ParseNodeExtraIPs(newNode.ExtraIPs.String), ",")
}

func nodeListenChanged(oldNode, newNode *model.Node) bool {
	return oldNode.TCPListenAddr != newNode.TCPListenAddr || oldNode.UDPListenAddr != newNode.UDPListenAddr ||
		strings.TrimSpace(oldNode.InterfaceName.String) != strings.TrimSpace(newNode.InterfaceName.String)
}

func rebuildNodeEntryInIP(entries []model.Node, ipPreference string) string {
	inNodes := make([]tunnelRuntimeNode, 0, len(entries))
	nodes := make(map[int64]*nodeRecord, len(entries))
	for _, entry := range entries {
		inNodes = append(inNodes, tunnelRuntimeNode{NodeID: entry.ID})
		nodes[entry.ID] = &nodeRecord{ID: entry.ID, ServerIP: entry.ServerIP,
			ServerIPv4: entry.ServerIPV4.String, ServerIPv6: entry.ServerIPV6.String}
	}
	return buildTunnelInIP(inNodes, nodes, ipPreference)
}

// The save has already succeeded. Synchronization errors are reported to the
// caller without turning the node edit into a failed request.
func (h *Handler) syncNodeUpdate(oldNode, newNode *model.Node) nodeSyncResult {
	result := nodeSyncResult{Failures: make([]nodeSyncFailure, 0)}
	addressChanged := nodeAddressChanged(oldNode, newNode)
	dialChanged := nodeDialAddressChanged(oldNode, newNode)
	listenChanged := nodeListenChanged(oldNode, newNode)
	var bindForwardIDs []int64
	if !addressChanged && !listenChanged {
		return result
	}

	var mu sync.Mutex
	tunnelsUpdated := make(map[int64]struct{})
	rulesUpdated := make(map[int64]struct{})
	pathsUpdated := 0
	fail := func(kind string, id int64, name string, err error) {
		mu.Lock()
		defer mu.Unlock()
		reason := normalizeBatchFailureReason(err.Error())
		result.Failures = append(result.Failures, nodeSyncFailure{Type: kind, ID: id, Name: name, Reason: reason})
		fmt.Printf("redeploy: %s %d failed on node %d: %v\n", kind, id, newNode.ID, err)
	}
	if addressChanged {
		rewrite, err := h.repo.RewriteNodeEntryAddresses(oldNode, newNode, rebuildNodeEntryInIP)
		if err != nil {
			fail("node", newNode.ID, newNode.Name, fmt.Errorf("入口地址同步失败：%w", err))
		} else {
			result.EntryAddressesUpdated = rewrite.EntryAddressesUpdated
			for _, id := range rewrite.TunnelIDs {
				tunnelsUpdated[id] = struct{}{}
			}
			for _, id := range rewrite.ForwardIDs {
				rulesUpdated[id] = struct{}{}
			}
			bindForwardIDs = rewrite.BindForwardIDs
		}
	}

	if newNode.Status == 1 && newNode.IsRemote != 1 {
		var tunnelIDs []int64
		var err error
		if listenChanged {
			tunnelIDs, err = h.repo.ListActiveTunnelIDsByNode(newNode.ID)
		} else if dialChanged {
			tunnelIDs, err = h.repo.ListActiveDialTargetTunnelIDsByNode(newNode.ID)
		}
		if err != nil {
			fail("node", newNode.ID, newNode.Name, fmt.Errorf("查询待同步隧道失败：%w", err))
		}
		coveredTunnels := make(map[int64]struct{}, len(tunnelIDs))
		jobs := make([]func(), 0, len(tunnelIDs))
		for _, tunnelID := range tunnelIDs {
			coveredTunnels[tunnelID] = struct{}{}
			jobs = append(jobs, func() {
				tunnelName, err := h.repo.GetTunnelDisplayName(tunnelID)
				if err != nil {
					fail("tunnel", tunnelID, "", err)
					return
				}
				ruleFailed := false
				err = h.redeployTunnelAndForwards(tunnelID, func(forward *model.ForwardRecord, syncErr error) {
					if syncErr != nil {
						ruleFailed = true
						fail("rule", forward.ID, forward.Name, syncErr)
						return
					}
					mu.Lock()
					rulesUpdated[forward.ID] = struct{}{}
					mu.Unlock()
				})
				if err != nil && !ruleFailed {
					fail("tunnel", tunnelID, tunnelName, err)
					return
				}
				mu.Lock()
				tunnelsUpdated[tunnelID] = struct{}{}
				mu.Unlock()
			})
		}
		var forwardIDs []int64
		if listenChanged {
			forwardIDs, err = h.repo.ListActiveForwardIDsByNode(newNode.ID)
			if err != nil {
				fail("node", newNode.ID, newNode.Name, fmt.Errorf("查询待同步规则失败：%w", err))
			}
		}
		// Rules whose listen IP was rewritten (a removed IP falls back to the
		// default listen-all address) must be redeployed to bind the new value.
		forwardIDs = mergeSortedIDs(forwardIDs, bindForwardIDs)
		for _, forwardID := range forwardIDs {
			forward, err := h.getForwardRecord(forwardID)
			if err != nil {
				fail("rule", forwardID, "", err)
				continue
			}
			if _, covered := coveredTunnels[forward.TunnelID]; covered {
				continue
			}
			jobs = append(jobs, func() {
				if err := h.syncNodeForwardServices(forward); err != nil {
					fail("rule", forward.ID, forward.Name, err)
					return
				}
				mu.Lock()
				rulesUpdated[forward.ID] = struct{}{}
				mu.Unlock()
			})
		}
		if dialChanged {
			paths, err := h.repo.ListActiveWGPathsByNode(newNode.ID)
			if err != nil {
				fail("node", newNode.ID, newNode.Name, fmt.Errorf("查询待同步 WireGuard 路径失败：%w", err))
			}
			for _, path := range paths {
				jobs = append(jobs, func() {
					if err := h.applyWGPath(path.ID); err != nil {
						fail("path", path.ID, path.Name, err)
						return
					}
					mu.Lock()
					pathsUpdated++
					mu.Unlock()
				})
			}
		}
		runNodeSyncJobs(jobs)
	}
	result.TunnelsUpdated = len(tunnelsUpdated) + pathsUpdated
	result.RulesUpdated = len(rulesUpdated)
	sort.Slice(result.Failures, func(i, j int) bool {
		if result.Failures[i].Type == result.Failures[j].Type {
			return result.Failures[i].ID < result.Failures[j].ID
		}
		return result.Failures[i].Type < result.Failures[j].Type
	})
	return result
}

func (h *Handler) syncNodeForwardServices(forward *model.ForwardRecord) error {
	warnings, err := h.syncForwardServicesWithWarnings(forward, "UpdateService", true)
	if err != nil {
		return err
	}
	if len(warnings) > 0 {
		return errors.New(strings.Join(warnings, "；"))
	}
	return nil
}

func runNodeSyncJobs(jobs []func()) {
	workers := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, job := range jobs {
		workers <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-workers }()
			job()
		}()
	}
	wg.Wait()
}

func mergeSortedIDs(a, b []int64) []int64 {
	seen := make(map[int64]struct{}, len(a)+len(b))
	out := make([]int64, 0, len(a)+len(b))
	for _, id := range append(append([]int64{}, a...), b...) {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
