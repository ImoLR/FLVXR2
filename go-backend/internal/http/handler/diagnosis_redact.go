package handler

import (
	"regexp"
	"strconv"
	"strings"
)

// Plan 225 (P2-1): a non-admin's rule 诊断 shows the hops between our nodes
// (入口→第N跳→出口) by node name only. Their address and port (the tunnel
// relay endpoint) stay hidden; the rule target / the user's own chain node
// (出口→目标) are the user's own data and stay visible.

var diagnosisAddrPattern = regexp.MustCompile(`\[?[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}\]?(?::\d{1,5})?|\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)

// diagnosisHopItem: the item probes one of our nodes (it has a target hop).
func diagnosisHopItem(item map[string]interface{}) bool {
	if item == nil {
		return false
	}
	_, ok := item["toChainType"]
	return ok
}

// redactDiagnosisItem hides a hop item's target address in place.
func redactDiagnosisItem(item map[string]interface{}) {
	if !diagnosisHopItem(item) {
		return
	}
	target := strings.TrimSpace(asString(item["targetIp"]))
	port := asInt(item["targetPort"], 0)
	item["targetIp"] = ""
	item["targetPort"] = 0
	for _, key := range []string{"message", "error"} {
		msg, ok := item[key].(string)
		if !ok || msg == "" {
			continue
		}
		msg = diagnosisAddrPattern.ReplaceAllString(msg, "***")
		if target != "" {
			msg = strings.ReplaceAll(msg, target, "***")
		}
		if port > 0 {
			msg = strings.ReplaceAll(msg, ":"+strconv.Itoa(port), "")
		}
		item[key] = msg
	}
}

func redactDiagnosisPayload(payload map[string]interface{}) {
	if payload == nil {
		return
	}
	switch list := payload["results"].(type) {
	case []map[string]interface{}:
		for _, item := range list {
			redactDiagnosisItem(item)
		}
	case []interface{}:
		for _, v := range list {
			if item, ok := v.(map[string]interface{}); ok {
				redactDiagnosisItem(item)
			}
		}
	}
}
