package ws

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Runtime revisions (plan 138).
//
// Every runtime object the panel pushes to an agent (gost service, chain,
// limiter, the nftables rule set of one forward) carries `_flvxRev`, a hash of
// the object's payload. Agents that advertise `runtimeRevs=1` remember the
// revision of each object they applied successfully in their current process
// and report the map through ListServices; on a reconnect the panel compares
// it with the payloads it would push and only re-sends what differs. Older
// agents ignore the field.

// RuntimeRevKey is the payload field that carries an object's revision.
const RuntimeRevKey = "_flvxRev"

// genericJSON converts a payload into plain JSON values (maps, slices,
// float64 numbers) so revisions hash the same bytes the agent receives.
func genericJSON(data interface{}) (interface{}, bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

func hashJSON(v interface{}) string {
	raw, err := json.Marshal(v) // map keys are sorted: canonical
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:12])
}

// revOfObject hashes one object without its own revision field.
func revOfObject(m map[string]interface{}) string {
	clean := make(map[string]interface{}, len(m))
	for k, v := range m {
		if k != RuntimeRevKey {
			clean[k] = v
		}
	}
	return hashJSON(clean)
}

// revOfLimiter covers only what the agent applies (name + limits), so the
// AddLimiters config and the UpdateLimiters envelope of the same limiter agree.
func revOfLimiter(m map[string]interface{}) string {
	return hashJSON(map[string]interface{}{"name": m["name"], "limits": m["limits"]})
}

func objectName(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func nftForwardIDs(m map[string]interface{}) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	rules, _ := m["rules"].([]interface{})
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
	return ids
}

// tagRuntimeRevs adds `_flvxRev` to the objects of a state-changing command
// (generic data, modified in place) and returns "<kind>:<name>" → revision of
// what an agent records after applying it successfully.
func tagRuntimeRevs(cmdType string, g interface{}) map[string]string {
	revs := map[string]string{}
	tag := func(kind string, m map[string]interface{}, rev string, nameKeys ...string) {
		if m == nil || rev == "" {
			return
		}
		m[RuntimeRevKey] = rev
		if name := objectName(m, nameKeys...); name != "" {
			revs[kind+":"+name] = rev
		}
	}
	switch cmdType {
	case "AddService", "UpdateService":
		switch v := g.(type) {
		case []interface{}:
			for _, raw := range v {
				if m, ok := raw.(map[string]interface{}); ok {
					tag("service", m, revOfObject(m), "name")
				}
			}
		case map[string]interface{}:
			tag("service", v, revOfObject(v), "name")
		}
	case "AddChains", "UpdateChains", "AddLimiters", "UpdateLimiters":
		m, ok := g.(map[string]interface{})
		if !ok {
			return revs
		}
		kind, envelope := "chain", "chain"
		if strings.HasSuffix(cmdType, "Limiters") {
			kind, envelope = "limiter", "limiter"
		}
		target := m
		if inner, ok := m["data"].(map[string]interface{}); ok {
			target = inner
			if objectName(inner, "name") == "" {
				if name := objectName(m, envelope); name != "" {
					inner["name"] = name
				}
			}
		}
		rev := revOfObject(target)
		if kind == "limiter" {
			rev = revOfLimiter(target)
		}
		tag(kind, target, rev, "name")
	case "SetEntryRates":
		// Plan 168: one object per node, the complete entry rate tree; an
		// empty tree carries no revision (= nothing applied on the agent).
		m, ok := g.(map[string]interface{})
		if !ok {
			return revs
		}
		classes, _ := m["classes"].([]interface{})
		ports, _ := m["ports"].([]interface{})
		if len(classes) == 0 && len(ports) == 0 {
			delete(m, RuntimeRevKey)
			return revs
		}
		rev := revOfObject(m)
		m[RuntimeRevKey] = rev
		revs["entryrate:entry"] = rev
	case "AddNftablesRules", "UpdateNftablesRules":
		m, ok := g.(map[string]interface{})
		if !ok {
			return revs
		}
		ids := nftForwardIDs(m)
		if len(ids) != 1 {
			return revs // several forwards in one request: no revision
		}
		rev := revOfObject(m)
		m[RuntimeRevKey] = rev
		revs[fmt.Sprintf("nft:%d_nft", ids[0])] = rev
	}
	return revs
}

func isRevCommand(cmdType string) bool {
	switch cmdType {
	case "AddService", "UpdateService", "AddChains", "UpdateChains", "AddLimiters", "UpdateLimiters", "AddNftablesRules", "UpdateNftablesRules", "SetEntryRates":
		return true
	}
	return false
}

// annotateRuntimeRevs returns the payload to send: a generic copy with
// revisions for state-changing commands, the original data otherwise.
func annotateRuntimeRevs(cmdType string, data interface{}) interface{} {
	if !isRevCommand(cmdType) {
		return data
	}
	g, ok := genericJSON(data)
	if !ok {
		return data
	}
	tagRuntimeRevs(cmdType, g)
	return g
}

// ExpectedRuntimeRevs returns "<kind>:<name>" → revision an agent records
// after applying cmdType with data (the same tagging the send path applies).
func ExpectedRuntimeRevs(cmdType string, data interface{}) map[string]string {
	if !isRevCommand(cmdType) {
		return map[string]string{}
	}
	g, ok := genericJSON(data)
	if !ok {
		return map[string]string{}
	}
	return tagRuntimeRevs(cmdType, g)
}
