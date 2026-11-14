package handler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/linechain"
)

// Plan 256: 「检测全部」 in the 订阅 page's 外部节点 dialog. A user without a
// landing node has no server to probe from (the 链式代理 检测 runs on the
// landing server), so this measures the TCP connect time from the panel. The
// address is checked like when the node was added (no panel / FLVX / private
// targets); UDP-only protocols (Hysteria2) cannot be checked this way. Run on
// demand only, a few at a time, per-user rate limited; nothing in background.

const (
	subProbeMax         = 50 // admins have no external node cap
	subProbeParallel    = 4
	subProbeDialTimeout = 3 * time.Second
	subProbeDNSTimeout  = 3 * time.Second
)

// subProbeDial: tests replace it.
var subProbeDial = func(ctx context.Context, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", address)
}

type subProbeResult struct {
	ID    int64  `json:"id"`
	OK    bool   `json:"ok"`
	Ms    int64  `json:"ms,omitempty"`
	UDP   bool   `json:"udp,omitempty"`
	Error string `json:"error,omitempty"`
}

func (h *Handler) subExternalProbe(w http.ResponseWriter, r *http.Request) {
	uid, _, err := h.subOwner(r)
	if err != nil {
		subFail(w, err)
		return
	}
	if !h.subState().probeRL.Take(strconv.FormatInt(uid, 10)) {
		subFail(w, errors.New("检测太频繁，请过半分钟再试"))
		return
	}
	nodes, err := h.repo.ListLandingExternalNodes(uid)
	if err != nil {
		subFail(w, errors.New("读取外部节点失败"))
		return
	}
	if len(nodes) > subProbeMax {
		nodes = nodes[:subProbeMax]
	}
	out := make([]subProbeResult, len(nodes))
	if len(nodes) == 0 {
		response.WriteJSON(w, response.OK(map[string]interface{}{"items": out}))
		return
	}
	forbidden := h.externalForbidden()
	sem := make(chan struct{}, subProbeParallel)
	var wg sync.WaitGroup
	for i, n := range nodes {
		out[i].ID = n.ID
		up, err := linechain.FromJSON(n.Upstream)
		if err != nil || up.Type == "" {
			out[i].Error = "节点数据损坏"
			continue
		}
		if up.UDPOnly() {
			out[i].UDP = true
			continue
		}
		wg.Add(1)
		go func(res *subProbeResult, up linechain.Upstream) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			dctx, cancel := context.WithTimeout(r.Context(), subProbeDNSTimeout)
			addrs, err := linechain.ValidateAddress(dctx, up.Server, forbidden, chainResolver)
			cancel()
			if err != nil || len(addrs) == 0 {
				res.Error = "地址检查未通过"
				if err != nil {
					res.Error = err.Error()
				}
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), subProbeDialTimeout)
			defer cancel()
			start := time.Now()
			c, err := subProbeDial(ctx, net.JoinHostPort(addrs[0].String(), strconv.Itoa(up.Port)))
			if err != nil {
				res.Error = "连接失败"
				if ctx.Err() != nil {
					res.Error = "超时"
				}
				return
			}
			ms := time.Since(start).Milliseconds()
			_ = c.Close()
			if ms < 1 {
				ms = 1
			}
			res.OK, res.Ms = true, ms
		}(&out[i], up)
	}
	wg.Wait()
	response.WriteJSON(w, response.OK(map[string]interface{}{"items": out}))
}
