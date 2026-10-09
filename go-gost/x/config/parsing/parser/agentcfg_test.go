package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testGostJSON = `{"services":[{"name":"svc1","addr":"127.0.0.1:18080","handler":{"type":"tcp"},"listener":{"type":"tcp"},"forwarder":{"nodes":[{"name":"n","addr":"127.0.0.1:18081"}]}}]}`

func agentJSON(tlsPart string) string {
	return `{"addr":"panel.example.com:443","secret":"s3cr3t","http":1,` + tlsPart + `"socks":1,"block_other":0,"node_id":48,"service_name":"flvxx"}`
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func parseWith(t *testing.T, choice AgentConfigChoice) ([]string, error) {
	t.Helper()
	Init(Args{CfgFile: choice.CfgFile, SkipDefaultLoad: choice.SkipDefaultLoad})
	cfg, err := Parse()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range cfg.Services {
		names = append(names, s.Name)
	}
	return names, nil
}

// The bug: an agent config with a "tls" int key is fatal for the gost parser.
func TestAgentConfigWithTLSKeyBreaksRawParse(t *testing.T) {
	for _, tlsPart := range []string{`"tls":0,`, `"tls":1,`} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.json")
		writeFile(t, cfgPath, agentJSON(tlsPart))
		if _, err := parseWith(t, AgentConfigChoice{CfgFile: cfgPath}); err == nil || !strings.Contains(err.Error(), "TLS") {
			t.Fatalf("%s: expected TLS decode error from raw parse, got %v", tlsPart, err)
		}
	}
}

func TestAgentConfigIgnoredLoadsSiblingGostJSON(t *testing.T) {
	for _, tlsPart := range []string{`"tls":0,`, `"tls":1,`, ``} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.json")
		writeFile(t, cfgPath, agentJSON(tlsPart))
		gostPath := filepath.Join(dir, "gost.json")
		writeFile(t, gostPath, testGostJSON)

		// agentConfigPath points elsewhere: detection via content heuristic.
		choice := ResolveAgentConfigFile(cfgPath, filepath.Join(t.TempDir(), "config.json"))
		if choice.CfgFile != gostPath || !choice.SkipDefaultLoad || choice.Note == "" {
			t.Fatalf("%q: unexpected choice %+v", tlsPart, choice)
		}
		names, err := parseWith(t, choice)
		if err != nil {
			t.Fatalf("%q: parse failed: %v", tlsPart, err)
		}
		if len(names) != 1 || names[0] != "svc1" {
			t.Fatalf("%q: expected service svc1, got %v", tlsPart, names)
		}
	}
}

func TestAgentConfigIgnoredNoGostJSONIsEmpty(t *testing.T) {
	for _, tlsPart := range []string{`"tls":0,`, `"tls":1,`, ``} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.json")
		writeFile(t, cfgPath, agentJSON(tlsPart))

		choice := ResolveAgentConfigFile(cfgPath, "")
		if choice.CfgFile != "" || !choice.SkipDefaultLoad || !strings.Contains(choice.Note, "empty") {
			t.Fatalf("%q: unexpected choice %+v", tlsPart, choice)
		}
		// Put a gost.json in the working directory: the default search must
		// NOT pick it up (stands in for /etc/gost or ~/.gost of another install).
		wd, _ := os.Getwd()
		other := t.TempDir()
		writeFile(t, filepath.Join(other, "gost.json"), testGostJSON)
		if err := os.Chdir(other); err != nil {
			t.Fatal(err)
		}
		names, err := parseWith(t, choice)
		_ = os.Chdir(wd)
		if err != nil {
			t.Fatalf("%q: parse failed: %v", tlsPart, err)
		}
		if len(names) != 0 {
			t.Fatalf("%q: expected no services, got %v", tlsPart, names)
		}
	}
}

func TestAgentConfigIgnoredBrokenGostJSONIsEmpty(t *testing.T) {
	for _, content := range []string{"", "{", "[1,2]", "null"} {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.json")
		writeFile(t, cfgPath, agentJSON(`"tls":1,`))
		writeFile(t, filepath.Join(dir, "gost.json"), content)
		choice := ResolveAgentConfigFile(cfgPath, "")
		if choice.CfgFile != "" || !choice.SkipDefaultLoad || !strings.Contains(choice.Note, "invalid") {
			t.Fatalf("%q: unexpected choice %+v", content, choice)
		}
		if _, err := parseWith(t, choice); err != nil {
			t.Fatalf("%q: parse failed: %v", content, err)
		}
	}
}

// Same file as the agent's config.json is ignored even if the content
// heuristic would not match (e.g. a symlink / no secret yet).
func TestAgentConfigDetectedBySameFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeFile(t, cfgPath, `{"addr":"panel.example.com:443","tls":1}`)
	link := filepath.Join(t.TempDir(), "agent.json")
	if err := os.Symlink(cfgPath, link); err != nil {
		t.Fatal(err)
	}
	choice := ResolveAgentConfigFile(link, cfgPath)
	if !choice.SkipDefaultLoad || choice.Note == "" {
		t.Fatalf("expected agent config detection via SameFile, got %+v", choice)
	}
	// Without the SameFile hint the file is not recognised (no secret).
	if c := ResolveAgentConfigFile(cfgPath, ""); c.SkipDefaultLoad || c.CfgFile != cfgPath {
		t.Fatalf("expected pass-through without hint, got %+v", c)
	}
}

func TestGenuineGostConfigUnchanged(t *testing.T) {
	dir := t.TempDir()
	gostPath := filepath.Join(dir, "my-gost.json")
	writeFile(t, gostPath, testGostJSON)
	// a sibling config.json must not matter
	writeFile(t, filepath.Join(dir, "config.json"), agentJSON(`"tls":1,`))

	choice := ResolveAgentConfigFile(gostPath, filepath.Join(dir, "config.json"))
	if choice != (AgentConfigChoice{CfgFile: gostPath}) {
		t.Fatalf("unexpected choice %+v", choice)
	}
	names, err := parseWith(t, choice)
	if err != nil || len(names) != 1 || names[0] != "svc1" {
		t.Fatalf("expected svc1, got %v err=%v", names, err)
	}

	// A gost config that also has addr/secret-like keys plus services stays gost.
	mixed := filepath.Join(dir, "mixed.json")
	writeFile(t, mixed, `{"addr":"x","secret":"y","services":[]}`)
	if c := ResolveAgentConfigFile(mixed, ""); c != (AgentConfigChoice{CfgFile: mixed}) {
		t.Fatalf("mixed: unexpected choice %+v", c)
	}

	// YAML gost config passes through.
	yml := filepath.Join(dir, "gost.yaml")
	writeFile(t, yml, "services:\n- name: svc1\n  addr: 127.0.0.1:18080\n  handler:\n    type: tcp\n  listener:\n    type: tcp\n")
	if c := ResolveAgentConfigFile(yml, ""); c != (AgentConfigChoice{CfgFile: yml}) {
		t.Fatalf("yaml: unexpected choice %+v", c)
	}

	// Missing file passes through (parser reports the error as before).
	missing := filepath.Join(dir, "nope.json")
	if c := ResolveAgentConfigFile(missing, ""); c != (AgentConfigChoice{CfgFile: missing}) {
		t.Fatalf("missing: unexpected choice %+v", c)
	}
}

func TestInlineAndEmptyCfgUnchanged(t *testing.T) {
	inline := `{"services":[{"name":"inl","addr":"127.0.0.1:18082","handler":{"type":"tcp"},"listener":{"type":"tcp"}}]}`
	if c := ResolveAgentConfigFile(inline, "config.json"); c != (AgentConfigChoice{CfgFile: inline}) {
		t.Fatalf("inline: unexpected choice %+v", c)
	}
	// inline JSON that looks like an agent config is still passed verbatim
	agentInline := agentJSON("")
	if c := ResolveAgentConfigFile(agentInline, ""); c != (AgentConfigChoice{CfgFile: agentInline}) {
		t.Fatalf("agent-like inline: unexpected choice %+v", c)
	}
	if c := ResolveAgentConfigFile("", "config.json"); c != (AgentConfigChoice{}) {
		t.Fatalf("empty: unexpected choice %+v", c)
	}
	names, err := parseWith(t, AgentConfigChoice{CfgFile: inline})
	if err != nil || len(names) != 1 || names[0] != "inl" {
		t.Fatalf("inline parse: %v err=%v", names, err)
	}
}
