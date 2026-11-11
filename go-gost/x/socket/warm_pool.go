package socket

import (
	"encoding/json"
	"fmt"

	"github.com/go-gost/x/warmpool"
)

// handleSetWarmPools (plan 244): the panel's full desired warm-pool state for
// this agent — {"chains": {"chains_25": 2}, "forwards": {"123": 2}}.
// Applied live: owners not listed lose their pooled conns at once, new ones
// are warmed within seconds. Nothing is persisted; the panel re-sends the
// state after every (re)connect.
func handleSetWarmPools(data interface{}) (interface{}, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("序列化数据失败: %v", err)
	}
	var cfg warmpool.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析请求失败: %v", err)
	}
	n := warmpool.Default.SetConfig(cfg)
	return map[string]interface{}{"owners": n}, nil
}

// warmChainChanged: a chain was added, swapped, reordered or deleted; its
// pool (if any) re-reads the next-hop order now.
func warmChainChanged(name string) {
	if warmpool.Default.Active(warmpool.KindChain, name) {
		warmpool.Default.OwnerChanged(warmpool.KindChain, name)
	}
}
