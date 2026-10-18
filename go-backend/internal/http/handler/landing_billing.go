package handler

import (
	"go-backend/internal/store/repo"
)

// Only the owner gets the landing exemption. Forward and user_tunnel counters keep
// their original tunnel bill; the user gets only the positive premium difference.
func (h *Handler) planLandingForwardBilling(b *flowIngestBatch, f *forwardRecord, entryID, up, down, in, out int64) error {
	userIn, userOut := in, out
	pathIn, pathOut := in, out
	var landingServerRef int64
	if f.LandingNodeID > 0 {
		n, err := h.repo.GetLandingNode(f.LandingNodeID)
		if err != nil {
			return err
		}
		if n != nil && n.UserID == f.UserID {
			s, err := h.repo.GetLandingServer(n.LandingServerID)
			if err != nil {
				return err
			}
			if s != nil {
				mode, ratio := repo.LandingRate(n, s)
				landingIn, landingOut := billTunnelFlow(mode, ratio, up, down)
				userIn = max(int64(0), in-landingIn)
				userOut = max(int64(0), out-landingOut)
				pathIn = max(in, landingIn)
				pathOut = max(out, landingOut)
				if s.Source == "native" {
					landingServerRef = s.NodeID
				}
				key := repo.ServerQuotaKey{UserID: f.UserID, Kind: "node", RefID: s.NodeID}
				if s.Source != "native" {
					key.Kind = "landing"
					key.RefID = s.ID
				}
				d := b.write.ServerQuotas[key]
				d.In += userIn
				d.Out += userOut
				b.write.ServerQuotas[key] = d
			}
		}
	}
	addFlowDelta(b.write.Users, f.UserID, userIn-in, userOut-out)
	b.write.QuotaUsage[f.UserID] += userIn + userOut - in - out
	weights, err := h.repo.ForwardServerWeights(f.TunnelID, entryID)
	if err != nil {
		return err
	}
	for nodeID, weight := range weights {
		// Landing's inbound counters already count this machine. Only premium extra
		// above was attributed here; do not count its relay/entry path a second time.
		if nodeID == landingServerRef {
			continue
		}
		key := repo.ServerQuotaKey{UserID: f.UserID, Kind: "node", RefID: nodeID}
		d := b.write.ServerQuotas[key]
		d.In += int64(float64(pathIn) * weight)
		d.Out += int64(float64(pathOut) * weight)
		b.write.ServerQuotas[key] = d
	}
	return nil
}
