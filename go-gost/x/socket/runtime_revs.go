package socket

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Runtime revisions (plan 138).
//
// The panel tags every runtime object it pushes (gost service, chain, limiter,
// nftables rule set of a forward) with `_flvxRev`, a hash of the object's
// payload. The agent remembers the revision of each object it applied
// successfully in THIS process and reports the map through ListServices. On a
// WebSocket reconnect the panel compares those revisions with the payloads it
// would push and only re-sends what differs, instead of deleting and re-adding
// every service (each re-add restarts the listener).
//
// The map is never persisted: after an agent restart it is empty, so the
// panel deploys everything once, exactly as before. Any command that touches
// an object without a revision (an older panel, pause/resume, delete, a failed
// apply) forgets that object's revision, so a stale revision can only cause an
// extra redeploy, never a skipped one.

const (
	runtimeRevKey = "_flvxRev"
	// runtimeRevsVersion is advertised in the WebSocket handshake
	// (`runtimeRevs=1`); the panel only diffs against agents that send it.
	runtimeRevsVersion = 1
)

// agentBootID identifies this agent process (reported with the revisions).
var agentBootID = newAgentBootID()

func newAgentBootID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

type runtimeRevStore struct {
	mu   sync.Mutex
	revs map[string]string // "<kind>:<name>" → revision
}

var runtimeRevs = &runtimeRevStore{revs: map[string]string{}}

func runtimeRevKeyOf(kind, name string) string { return kind + ":" + name }

func (s *runtimeRevStore) set(kind, name, rev string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	if rev == "" {
		delete(s.revs, runtimeRevKeyOf(kind, name))
	} else {
		s.revs[runtimeRevKeyOf(kind, name)] = rev
	}
	s.mu.Unlock()
}

func (s *runtimeRevStore) drop(kind string, names ...string) {
	s.mu.Lock()
	for _, name := range names {
		delete(s.revs, runtimeRevKeyOf(kind, name))
	}
	s.mu.Unlock()
}

func (s *runtimeRevStore) get(kind, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revs[runtimeRevKeyOf(kind, name)]
}

// snapshot returns the revisions of the objects present in inv only (a removed
// object never keeps a revision in the report).
func (s *runtimeRevStore) snapshot(inv serviceInventory) map[string]string {
	present := map[string]bool{}
	for kind, names := range map[string][]string{"service": inv.Services, "chain": inv.Chains, "limiter": inv.Limiters, "nft": inv.Nftables} {
		for _, name := range names {
			present[runtimeRevKeyOf(kind, name)] = true
		}
	}
	out := map[string]string{}
	s.mu.Lock()
	for key, rev := range s.revs {
		if present[key] {
			out[key] = rev
		}
	}
	s.mu.Unlock()
	return out
}

func (s *runtimeRevStore) reset() {
	s.mu.Lock()
	s.revs = map[string]string{}
	s.mu.Unlock()
}

type revItem struct {
	name string
	rev  string
}

// itemRev reads name + revision of one payload object. nameKeys lists the
// fields that may carry the object name.
func itemRev(m map[string]interface{}, nameKeys ...string) revItem {
	var it revItem
	for _, key := range nameKeys {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			it.name = strings.TrimSpace(v)
			break
		}
	}
	if v, ok := m[runtimeRevKey].(string); ok {
		it.rev = strings.TrimSpace(v)
	}
	return it
}

// genericData turns command data into plain JSON values (maps/slices).
func genericData(data interface{}) interface{} {
	switch data.(type) {
	case map[string]interface{}, []interface{}, string, nil:
		return data
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var out interface{}
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// serviceRevItems: AddService/UpdateService carry a list of service configs.
func serviceRevItems(data interface{}) []revItem {
	var items []revItem
	switch v := genericData(data).(type) {
	case []interface{}:
		for _, raw := range v {
			if m, ok := raw.(map[string]interface{}); ok {
				items = append(items, itemRev(m, "name"))
			}
		}
	case map[string]interface{}:
		items = append(items, itemRev(v, "name"))
	}
	return items
}

// namedRevItem: chains/limiters are one object, either the config itself
// ({"name":…}) or an update envelope ({"chain"|"limiter": name, "data": {…}}).
func namedRevItem(data interface{}, envelopeKey string) []revItem {
	m, ok := genericData(data).(map[string]interface{})
	if !ok {
		if s, ok := data.(string); ok && strings.TrimSpace(s) != "" {
			return []revItem{{name: strings.TrimSpace(s)}}
		}
		return nil
	}
	if inner, ok := m["data"].(map[string]interface{}); ok {
		it := itemRev(inner, "name")
		if it.name == "" {
			it.name = itemRev(m, envelopeKey).name
		}
		return []revItem{it}
	}
	return []revItem{itemRev(m, "name", envelopeKey)}
}

// listNames reads {"<key>": [names…]} (DeleteService/PauseService/ResumeService).
func listNames(data interface{}, key string) []string {
	m, ok := genericData(data).(map[string]interface{})
	if !ok {
		return nil
	}
	var names []string
	switch v := m[key].(type) {
	case []interface{}:
		for _, raw := range v {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				names = append(names, strings.TrimSpace(s))
			}
		}
	case string:
		if strings.TrimSpace(v) != "" {
			names = append(names, strings.TrimSpace(v))
		}
	}
	return names
}

func nftForwardName(id int64) string { return fmt.Sprintf("%d_nft", id) }

// nftRevItems: the panel tags an AddNftablesRules/UpdateNftablesRules request
// of ONE forward with a top-level revision. A request covering several
// forwards (or none) records nothing and forgets their revisions.
func nftRevItems(data interface{}) (items []revItem, all []string) {
	m, ok := genericData(data).(map[string]interface{})
	if !ok {
		return nil, nil
	}
	seen := map[int64]bool{}
	var ids []int64
	if rules, ok := m["rules"].([]interface{}); ok {
		for _, raw := range rules {
			r, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if f, ok := r["forward_id"].(float64); ok && f > 0 && !seen[int64(f)] {
				seen[int64(f)] = true
				ids = append(ids, int64(f))
			}
		}
	}
	for _, id := range ids {
		all = append(all, nftForwardName(id))
	}
	rev, _ := m[runtimeRevKey].(string)
	if len(ids) == 1 && strings.TrimSpace(rev) != "" {
		return []revItem{{name: nftForwardName(ids[0]), rev: strings.TrimSpace(rev)}}, all
	}
	return nil, all
}

func nftDeleteNames(data interface{}) []string {
	m, ok := genericData(data).(map[string]interface{})
	if !ok {
		return nil
	}
	var names []string
	if ids, ok := m["forward_ids"].([]interface{}); ok {
		for _, raw := range ids {
			if f, ok := raw.(float64); ok && f > 0 {
				names = append(names, nftForwardName(int64(f)))
			}
		}
	}
	return names
}

// recordRuntimeRevs updates the revision map after a command ran (err = the
// handler's result). Unknown commands leave the map alone.
func recordRuntimeRevs(cmdType string, data interface{}, err error) {
	apply := func(kind string, items []revItem) {
		for _, it := range items {
			if err == nil {
				runtimeRevs.set(kind, it.name, it.rev)
			} else {
				runtimeRevs.drop(kind, it.name)
			}
		}
	}
	switch cmdType {
	case "AddService", "UpdateService":
		apply("service", serviceRevItems(data))
	case "DeleteService", "PauseService", "ResumeService":
		runtimeRevs.drop("service", listNames(data, "services")...)
	case "AddChains", "UpdateChains":
		apply("chain", namedRevItem(data, "chain"))
	case "DeleteChains":
		for _, it := range namedRevItem(data, "chain") {
			runtimeRevs.drop("chain", it.name)
		}
	case "AddLimiters", "UpdateLimiters":
		apply("limiter", namedRevItem(data, "limiter"))
	case "DeleteLimiters":
		for _, it := range namedRevItem(data, "limiter") {
			runtimeRevs.drop("limiter", it.name)
		}
	case "AddNftablesRules", "UpdateNftablesRules":
		items, all := nftRevItems(data)
		if err != nil || len(items) == 0 {
			runtimeRevs.drop("nft", all...)
			return
		}
		apply("nft", items)
	case "DeleteNftablesRules":
		runtimeRevs.drop("nft", nftDeleteNames(data)...)
	}
}
