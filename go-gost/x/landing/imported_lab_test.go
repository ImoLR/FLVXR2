package landing

import (
	"encoding/json"
	"os"
	"testing"
)

// Plan 291 lab helper: FLVX_IMPORT_LAB_IN = JSON list of Nodes; writes the
// Core config the agent would render (base + inbounds) to FLVX_IMPORT_LAB_OUT.
func TestImportedLabRender(t *testing.T) {
	in, out := os.Getenv("FLVX_IMPORT_LAB_IN"), os.Getenv("FLVX_IMPORT_LAB_OUT")
	if in == "" || out == "" {
		t.Skip("lab files not supplied")
	}
	raw, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []Node
	if err := json.Unmarshal(raw, &nodes); err != nil {
		t.Fatal(err)
	}
	d := Desired{Revision: 1, Enabled: true, Nodes: nodes}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig(39990)
	inbounds := []any{}
	for _, n := range d.Nodes {
		ib, err := n.inbound()
		if err != nil {
			t.Fatal(err)
		}
		inbounds = append(inbounds, ib)
	}
	cfg["inbounds"] = inbounds
	b, _ := json.MarshalIndent(cfg, "", " ")
	if err := os.WriteFile(out, b, 0600); err != nil {
		t.Fatal(err)
	}
}
