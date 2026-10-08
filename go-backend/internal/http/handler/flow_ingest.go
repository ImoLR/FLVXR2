package handler

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Tunnel billing modes (tunnel.flow). The mode is also the billing multiplier.
const (
	tunnelFlowOneWay int64 = 1 // 单向: (upload + download) x ratio
	tunnelFlowTwoWay int64 = 2 // 双向: (upload + download) x ratio x 2
)

// billTunnelFlow converts one flow report into billed (in_flow, out_flow) increments,
// using the original panel formula: each direction x ratio x flow mode.
//
// upload is what the client sent (agent item D), download is what it received (agent item U).
//   - in = upload*ratio*mode, out = download*ratio*mode (ratio applied first, truncated, then
//     multiplied by the mode), so 单向 bills (upload+download)*ratio and 双向 twice that.
//
// A ratio <= 0 and a mode <= 0 are treated as 1.
func billTunnelFlow(flowMode int64, ratio float64, upload, download int64) (int64, int64) {
	if upload < 0 {
		upload = 0
	}
	if download < 0 {
		download = 0
	}
	if ratio <= 0 {
		ratio = 1
	}
	if flowMode <= 0 {
		flowMode = 1
	}
	scale := func(v int64) int64 { return int64(float64(v)*ratio) * flowMode }
	return scale(upload), scale(download)
}

// billFlowForTunnel bills with the tunnel's mode and ratio; without a tunnel the raw
// upload/download bytes are recorded.
func billFlowForTunnel(tunnel *tunnelRecord, upload, download int64) (int64, int64) {
	if tunnel == nil {
		return billTunnelFlow(tunnelFlowOneWay, 1, upload, download)
	}
	return billTunnelFlow(tunnel.Flow, tunnel.TrafficRatio, upload, download)
}

type flowPolicyCheck struct {
	userID       int64
	userTunnelID int64
}

// flowIngestBatch collects the writes and follow-up checks of one agent flow upload.
type flowIngestBatch struct {
	write repo.FlowBatch

	quotaUsers    []int64
	forwardChecks []int64
	policyChecks  []flowPolicyCheck
	shareChecks   []int64

	seenQuotaUsers map[int64]struct{}
	seenForwards   map[int64]struct{}
	seenPolicies   map[flowPolicyCheck]struct{}
	seenShares     map[int64]struct{}
}

func newFlowIngestBatch() *flowIngestBatch {
	return &flowIngestBatch{
		write: repo.FlowBatch{
			Forwards:    map[int64]repo.FlowCounterDelta{},
			Users:       map[int64]repo.FlowCounterDelta{},
			UserTunnels: map[int64]repo.FlowCounterDelta{},
			QuotaUsage:  map[int64]int64{},
			PeerShares:  map[int64]int64{},
		},
		seenQuotaUsers: map[int64]struct{}{},
		seenForwards:   map[int64]struct{}{},
		seenPolicies:   map[flowPolicyCheck]struct{}{},
		seenShares:     map[int64]struct{}{},
	}
}

func addFlowDelta(m map[int64]repo.FlowCounterDelta, id, in, out int64) {
	if id <= 0 {
		return
	}
	d := m[id]
	d.In += in
	d.Out += out
	m[id] = d
}

// addLocalFlow adds billed bytes to a forward (when it exists), its user, user_tunnel and the
// user's daily/monthly quota (when the user exists).
func (b *flowIngestBatch) addLocalFlow(forwardID, userID, userTunnelID, in, out int64, forwardExists, userExists bool) {
	if forwardExists {
		addFlowDelta(b.write.Forwards, forwardID, in, out)
		if _, ok := b.seenForwards[forwardID]; !ok {
			b.seenForwards[forwardID] = struct{}{}
			b.forwardChecks = append(b.forwardChecks, forwardID)
		}
	}
	addFlowDelta(b.write.Users, userID, in, out)
	if userTunnelID > 0 {
		addFlowDelta(b.write.UserTunnels, userTunnelID, in, out)
		check := flowPolicyCheck{userID: userID, userTunnelID: userTunnelID}
		if _, ok := b.seenPolicies[check]; !ok {
			b.seenPolicies[check] = struct{}{}
			b.policyChecks = append(b.policyChecks, check)
		}
	}
	if userID > 0 && userExists {
		b.write.QuotaUsage[userID] += in + out
		if _, ok := b.seenQuotaUsers[userID]; !ok {
			b.seenQuotaUsers[userID] = struct{}{}
			b.quotaUsers = append(b.quotaUsers, userID)
		}
	}
}

func (b *flowIngestBatch) addShareFlow(shareID, delta int64) {
	if shareID <= 0 || delta <= 0 {
		return
	}
	b.write.PeerShares[shareID] += delta
	if _, ok := b.seenShares[shareID]; !ok {
		b.seenShares[shareID] = struct{}{}
		b.shareChecks = append(b.shareChecks, shareID)
	}
}

// flowIngestCache memoizes the lookups of one upload (tcp and udp items share a forward).
type flowIngestCache struct {
	repo        *repo.Repository
	forwards    map[int64]*forwardRecord
	tunnels     map[int64]*tunnelRecord
	tunnelNames map[int64]string
	userTunnels map[[2]int64]int64
	utRecords   map[int64]*model.UserTunnel
	users       map[int64]bool
}

func newFlowIngestCache(r *repo.Repository) *flowIngestCache {
	return &flowIngestCache{
		repo:        r,
		forwards:    map[int64]*forwardRecord{},
		tunnels:     map[int64]*tunnelRecord{},
		tunnelNames: map[int64]string{},
		userTunnels: map[[2]int64]int64{},
		utRecords:   map[int64]*model.UserTunnel{},
		users:       map[int64]bool{},
	}
}

// userExists reports whether the user still exists.
func (c *flowIngestCache) userExists(id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	if v, ok := c.users[id]; ok {
		return v, nil
	}
	u, err := c.repo.GetUserByID(id)
	if err != nil {
		return false, err
	}
	c.users[id] = u != nil
	return u != nil, nil
}

// forward returns (nil, nil) when the forward does not exist.
func (c *flowIngestCache) forward(id int64) (*forwardRecord, error) {
	if v, ok := c.forwards[id]; ok {
		return v, nil
	}
	v, err := c.repo.GetForwardRecord(id)
	if err != nil {
		return nil, err
	}
	c.forwards[id] = v
	return v, nil
}

// tunnel returns (nil, nil) when the tunnel does not exist.
func (c *flowIngestCache) tunnel(id int64) (*tunnelRecord, error) {
	if v, ok := c.tunnels[id]; ok {
		return v, nil
	}
	v, err := c.repo.GetTunnelRecord(id)
	if err != nil {
		return nil, err
	}
	c.tunnels[id] = v
	return v, nil
}

func (c *flowIngestCache) tunnelName(id int64) (string, error) {
	if v, ok := c.tunnelNames[id]; ok {
		return v, nil
	}
	v, err := c.repo.GetTunnelName(id)
	if err != nil {
		return "", err
	}
	c.tunnelNames[id] = v
	return v, nil
}

// userTunnelID resolves the user_tunnel a user's forward on tunnelID is billed to, the same
// way service names are built (lowest id); 0 when the user has none (e.g. admin).
func (c *flowIngestCache) userTunnelID(userID, tunnelID int64) (int64, error) {
	key := [2]int64{userID, tunnelID}
	if v, ok := c.userTunnels[key]; ok {
		return v, nil
	}
	info, err := c.repo.ResolveUserTunnelAndLimiter(userID, tunnelID)
	if err != nil {
		return 0, err
	}
	var id int64
	if info != nil {
		id = info.UserTunnelID
	}
	c.userTunnels[key] = id
	return id, nil
}

// userTunnelRecord returns (nil, nil) when the user_tunnel does not exist.
func (c *flowIngestCache) userTunnelRecord(id int64) (*model.UserTunnel, error) {
	if v, ok := c.utRecords[id]; ok {
		return v, nil
	}
	v, err := c.repo.GetUserTunnelByID(id)
	if err != nil {
		return nil, err
	}
	c.utRecords[id] = v
	return v, nil
}

// processFlowItem ingests a single flow item; see ingestFlowItems.
func (h *Handler) processFlowItem(nodeID int64, item flowItem) error {
	return h.ingestFlowItems(nodeID, []flowItem{item})
}

// ingestFlowItems processes one agent flow upload and runs the follow-up enforcement before
// returning; see commitFlowItems.
func (h *Handler) ingestFlowItems(nodeID int64, items []flowItem) error {
	enforce, err := h.commitFlowItems(nodeID, items)
	if err != nil {
		return err
	}
	if enforce != nil {
		enforce()
	}
	return nil
}

// commitFlowItems writes every counter update of one agent flow upload in a single
// transaction: if it fails nothing is written and the error is returned, so the agent keeps
// the bytes and resends them. Tunnel metrics are recorded after a successful commit. The
// returned function runs the follow-up enforcement (quota, traffic limit, flow policy and peer
// share pauses); it may send node commands and take a while, so /flow/upload runs it after
// answering the agent.
func (h *Handler) commitFlowItems(nodeID int64, items []flowItem) (func(), error) {
	if h == nil || h.repo == nil {
		return nil, errors.New("repository not initialized")
	}
	if len(items) == 0 {
		return nil, nil
	}

	batch := newFlowIngestBatch()
	cache := newFlowIngestCache(h.repo)
	for _, item := range items {
		if err := h.planFlowItem(batch, cache, nodeID, item); err != nil {
			return nil, fmt.Errorf("resolve flow item %q: %w", item.N, err)
		}
	}

	now := time.Now()
	quotas, err := h.repo.ApplyFlowBatch(batch.write, now)
	if err != nil {
		return nil, fmt.Errorf("apply flow batch: %w", err)
	}

	h.recordTunnelMetricsFromFlowItems(nodeID, items, now.UnixMilli())
	return func() {
		// Uploads of several nodes may finish together; checks of the same forward or user
		// must not pause and reset it twice.
		h.flowEnforceMu.Lock()
		defer h.flowEnforceMu.Unlock()
		h.enforceFlowBatch(batch, quotas)
	}, nil
}

func (h *Handler) planFlowItem(b *flowIngestBatch, c *flowIngestCache, nodeID int64, item flowItem) error {
	serviceName := strings.TrimSpace(item.N)
	if serviceName == "" || serviceName == "web_api" {
		return nil
	}
	upload, download := item.D, item.U
	if upload < 0 {
		upload = 0
	}
	if download < 0 {
		download = 0
	}

	if forwardID, userID, userTunnelID, ok := parseFlowServiceIDs(serviceName); ok {
		return h.planForwardFlow(b, c, nodeID, serviceName, forwardID, userID, userTunnelID, upload, download)
	}

	runtimeID, ok := parsePeerShareRuntimeServiceID(serviceName)
	if !ok {
		return nil
	}
	runtime, err := h.repo.GetPeerShareRuntimeByID(runtimeID)
	if err != nil {
		return err
	}
	if runtime == nil || runtime.ShareID <= 0 || runtime.Status != 1 {
		return nil
	}
	b.addShareFlow(runtime.ShareID, upload+download)
	return nil
}

// planForwardFlow bills a forward service's traffic. Ownership is taken from the database,
// not from the ids embedded in the service name: a node that missed a resync (tunnel move,
// user_tunnel re-created by a group revoke/re-grant) still reports the old user_tunnel id.
// The parsed ids are only used when the forward no longer exists.
func (h *Handler) planForwardFlow(b *flowIngestBatch, c *flowIngestCache, nodeID int64, serviceName string, forwardID, parsedUserID, parsedUserTunnelID, upload, download int64) error {
	forward, err := c.forward(forwardID)
	if err != nil {
		return err
	}

	if forward != nil && forward.UserID == parsedUserID {
		userTunnelID, err := c.userTunnelID(forward.UserID, forward.TunnelID)
		if err != nil {
			return err
		}
		tunnel, err := c.tunnel(forward.TunnelID)
		if err != nil {
			return err
		}
		in, out := billFlowForTunnel(tunnel, upload, download)
		b.addLocalFlow(forward.ID, forward.UserID, userTunnelID, in, out, true, true)
		return h.planForwardPeerShareFlow(b, c, nodeID, serviceName, forward, upload+download)
	}

	// The name is not (or no longer) one of this panel's forwards. Peer share runtimes that
	// another panel runs on this node use the same "{forward}_{user}_{user_tunnel}" format of
	// that panel: their traffic only counts towards the peer share.
	runtime, err := h.matchForwardPeerShareRuntime(nodeID, serviceName)
	if err != nil {
		return err
	}
	if runtime != nil {
		b.addShareFlow(runtime.ShareID, upload+download)
		return nil
	}
	if forward != nil {
		// A forward with this id exists but belongs to someone else (e.g. the id was reused):
		// it is not this forward's traffic.
		return nil
	}

	// The forward was deleted while the node still had unreported bytes: bill them to the
	// owner named by the service, with the tunnel of that user_tunnel when it still exists.
	userExists, err := c.userExists(parsedUserID)
	if err != nil {
		return err
	}
	if !userExists {
		return nil
	}
	var tunnel *tunnelRecord
	userTunnelID := int64(0)
	if parsedUserTunnelID > 0 {
		ut, err := c.userTunnelRecord(parsedUserTunnelID)
		if err != nil {
			return err
		}
		if ut != nil && ut.UserID == parsedUserID {
			userTunnelID = ut.ID
			if tunnel, err = c.tunnel(ut.TunnelID); err != nil {
				return err
			}
		}
	}
	in, out := billFlowForTunnel(tunnel, upload, download)
	b.addLocalFlow(forwardID, parsedUserID, userTunnelID, in, out, false, true)
	return nil
}

// planForwardPeerShareFlow attributes a local forward's raw traffic to a peer share: by the
// forward's federation tunnel name ("Share-{id}-Port-{port}") or else by the runtime service name.
func (h *Handler) planForwardPeerShareFlow(b *flowIngestBatch, c *flowIngestCache, nodeID int64, serviceName string, forward *forwardRecord, delta int64) error {
	if delta <= 0 {
		return nil
	}
	if forward != nil {
		if name, err := c.tunnelName(forward.TunnelID); err == nil {
			if shareID, ok := parsePeerShareIDFromFederationTunnelName(name); ok {
				b.addShareFlow(shareID, delta)
				return nil
			}
		}
	}
	return h.planPeerShareFlowByServiceName(b, nodeID, serviceName, delta)
}

func (h *Handler) planPeerShareFlowByServiceName(b *flowIngestBatch, nodeID int64, serviceName string, delta int64) error {
	runtime, err := h.matchForwardPeerShareRuntime(nodeID, serviceName)
	if err != nil {
		return err
	}
	if runtime != nil {
		b.addShareFlow(runtime.ShareID, delta)
	}
	return nil
}

// matchForwardPeerShareRuntime finds the single active forward peer share runtime that runs
// serviceName: on this node first, then on any node. Ambiguous matches return nil.
func (h *Handler) matchForwardPeerShareRuntime(nodeID int64, serviceName string) (*model.PeerShareRuntime, error) {
	if strings.TrimSpace(serviceName) == "" {
		return nil, nil
	}

	normalized := normalizeForwardRuntimeServiceName(serviceName)
	var runtimes []model.PeerShareRuntime
	var err error

	// Try the node-scoped query first, then fall back to a global match.
	if nodeID > 0 {
		runtimes, err = h.repo.ListActiveForwardPeerShareRuntimesByNodeAndServiceName(nodeID, normalized)
		if err != nil {
			return nil, err
		}
		if len(runtimes) == 0 && normalized != serviceName {
			runtimes, err = h.repo.ListActiveForwardPeerShareRuntimesByNodeAndServiceName(nodeID, serviceName)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(runtimes) == 0 {
		runtimes, err = h.repo.ListActiveForwardPeerShareRuntimesByServiceName(normalized)
		if err != nil {
			return nil, err
		}
		if len(runtimes) == 0 && normalized != serviceName {
			runtimes, err = h.repo.ListActiveForwardPeerShareRuntimesByServiceName(serviceName)
			if err != nil {
				return nil, err
			}
		}
	}

	if len(runtimes) != 1 {
		if len(runtimes) > 1 {
			log.Printf("WARN: ambiguous peer share runtime match for service=%s nodeID=%d count=%d", serviceName, nodeID, len(runtimes))
		}
		return nil, nil
	}
	runtime := runtimes[0]
	return &runtime, nil
}

func (h *Handler) enforceFlowBatch(b *flowIngestBatch, quotas map[int64]*model.UserQuotaView) {
	for _, userID := range b.quotaUsers {
		if quota := quotas[userID]; quota != nil {
			h.enforceUserQuotaIfNeeded(userID, quota)
		}
	}
	for _, shareID := range b.shareChecks {
		share, err := h.repo.GetPeerShare(shareID)
		if err != nil || share == nil || !isPeerShareFlowExceeded(share) {
			continue
		}
		h.enforcePeerShareFlowLimit(share.ID)
	}
	for _, forwardID := range b.forwardChecks {
		h.enforceForwardTrafficLimit(forwardID)
	}
	for _, check := range b.policyChecks {
		h.enforceFlowPolicies(check.userID, check.userTunnelID)
	}
}
