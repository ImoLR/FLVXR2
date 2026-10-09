package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gostConfigKeys are the top-level keys of a gost config (config.Config).
// "tls" is deliberately missing: the agent config uses it too (int flag).
var gostConfigKeys = map[string]struct{}{
	"services": {}, "chains": {}, "hops": {}, "authers": {}, "admissions": {},
	"bypasses": {}, "resolvers": {}, "hosts": {}, "ingresses": {}, "routers": {},
	"sds": {}, "recorders": {}, "limiters": {}, "climiters": {}, "rlimiters": {},
	"observers": {}, "loggers": {}, "log": {}, "profiling": {}, "api": {}, "metrics": {},
}

// AgentConfigChoice is what the gost parser should load for a -C value.
type AgentConfigChoice struct {
	CfgFile         string // value for Args.CfgFile
	SkipDefaultLoad bool   // value for Args.SkipDefaultLoad
	Note            string // one log line when -C was overridden, else ""
}

// ResolveAgentConfigFile decides what the gost parser loads for the -C flag.
//
// install.sh writes units with `-C <dir>/config.json`, i.e. it hands the AGENT
// config to the gost parser. The agent config has an int "tls" key (written
// by the agent itself when the panel pushes protocol settings) that collides
// with gost's top-level `tls` map, which makes every restart fatal. When -C
// names the agent config, it is ignored: <dir>/gost.json is used if it is a
// valid JSON object, otherwise the config starts empty (services come from the
// panel). The default search path is disabled in both cases so that a gost.json
// of an unrelated gost install (/etc/gost, ~/.gost) is never picked up.
//
// Inline JSON, an empty value and genuine gost config files are passed through
// unchanged.
func ResolveAgentConfigFile(cfgFile, agentConfigPath string) AgentConfigChoice {
	trimmed := strings.TrimSpace(cfgFile)
	if trimmed == "" || (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) {
		return AgentConfigChoice{CfgFile: cfgFile}
	}
	if !isAgentConfigFile(trimmed, agentConfigPath) {
		return AgentConfigChoice{CfgFile: cfgFile}
	}

	gostFile := filepath.Join(filepath.Dir(trimmed), "gost.json")
	data, err := os.ReadFile(gostFile)
	if err != nil {
		if os.IsNotExist(err) {
			return AgentConfigChoice{
				SkipDefaultLoad: true,
				Note:            fmt.Sprintf("-C %s is the agent config, ignored; %s not found, starting with an empty gost config", trimmed, gostFile),
			}
		}
		return AgentConfigChoice{
			SkipDefaultLoad: true,
			Note:            fmt.Sprintf("-C %s is the agent config, ignored; cannot read %s (%v), starting with an empty gost config", trimmed, gostFile, err),
		}
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		reason := "not a JSON object"
		if err != nil {
			reason = err.Error()
		}
		return AgentConfigChoice{
			SkipDefaultLoad: true,
			Note:            fmt.Sprintf("-C %s is the agent config, ignored; %s is invalid (%s), starting with an empty gost config", trimmed, gostFile, reason),
		}
	}
	return AgentConfigChoice{
		CfgFile:         gostFile,
		SkipDefaultLoad: true,
		Note:            fmt.Sprintf("-C %s is the agent config, ignored; loading %s instead", trimmed, gostFile),
	}
}

// isAgentConfigFile reports whether path is the FLVX agent config: the same
// file the agent loaded, or a JSON object with addr+secret and no gost keys.
func isAgentConfigFile(path, agentConfigPath string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false // let the gost parser report a missing/unreadable file as before
	}
	if agentConfigPath != "" {
		if ast, err := os.Stat(agentConfigPath); err == nil && os.SameFile(st, ast) {
			return true
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	hasAddr, hasSecret := false, false
	for k := range obj {
		key := strings.ToLower(k)
		if _, ok := gostConfigKeys[key]; ok {
			return false
		}
		switch key {
		case "addr":
			hasAddr = true
		case "secret":
			hasSecret = true
		}
	}
	return hasAddr && hasSecret
}
