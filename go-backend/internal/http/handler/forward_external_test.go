package handler

import (
	"strings"
	"testing"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
)

// Plan 284 handler tests: 线路机 chains from a 外部节点 id, update rules and
// backup import normalisation.

func seedExternal(t *testing.T, e *flowTestEnv, owner int64, name, link string) int64 {
	t.Helper()
	up, _, err := linechain.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	n := externalModel(up, name)
	n.UserID = owner
	if err := e.r.DB().Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n.ID
}

// A chain created from a list id is the same chain as pasting the link.
func TestChainFromExternalIDMatchesPastedLink(t *testing.T) {
	e, agent := newChainEnv(t)
	extID := seedExternal(t, e, 7, "home", testSSLink)
	pasted := createChain(t, e, 20, "pasted", testSSLink, "key-00000101")
	pastedNodes := agent.chainNodes(6)

	body := chainCreateBody(10, "from-list", "", "key-00000102")
	body["chainExternalId"] = extID
	code, msg, data := callAs(t, e.h.forwardCreate, 7, 1, body)
	if code != 0 {
		t.Fatalf("create from list: %d %s", code, msg)
	}
	e.h.chains.wg.Wait()
	listed, err := e.r.GetForwardChain(asInt64(data["chainId"], 0))
	if err != nil || listed == nil || listed.State != model.ChainStateActive {
		t.Fatalf("chain %+v %v", listed, err)
	}
	if listed.UpstreamHash != pasted.UpstreamHash || listed.UpstreamProtocol != pasted.UpstreamProtocol || listed.UpstreamHost != pasted.UpstreamHost || listed.UpstreamPort != pasted.UpstreamPort {
		t.Fatalf("upstream differs:\n%+v\n%+v", listed, pasted)
	}
	a, _ := linechain.FromJSON(listed.Upstream)
	b, _ := linechain.FromJSON(pasted.Upstream)
	a.Tag, b.Tag = "", ""
	if a.JSON() != b.JSON() {
		t.Fatalf("upstream json %s vs %s", a.JSON(), b.JSON())
	}
	for _, nodeID := range []int64{3, 4} {
		nodes := agent.chainNodes(nodeID)
		if len(nodes) != 1 {
			t.Fatalf("exit %d nodes %v", nodeID, nodes)
		}
		got := nodes[0]["chain"].(map[string]interface{})
		want := pastedNodes[0]["chain"].(map[string]interface{})
		for k, v := range want {
			if k != "tag" && asString(got[k]) != asString(v) {
				t.Fatalf("exit %d chain[%s] = %v, want %v", nodeID, k, got[k], v)
			}
		}
	}
	// The forward of a chain is not linked to the list (its upstream is a copy).
	f, _ := e.h.getForwardRecord(listed.ForwardID)
	if f.ExternalNodeID != 0 {
		t.Fatalf("chain forward linked: %+v", f)
	}

	// Another user's node is refused.
	e.addUser(8)
	foreign := seedExternal(t, e, 8, "x", "socks5://u:p@8.8.8.8:1080#x")
	body = chainCreateBody(20, "foreign", "", "key-00000103")
	body["chainExternalId"] = foreign
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, body); code == 0 || !strings.Contains(msg, "外部节点不存在") {
		t.Fatalf("foreign external: %d %s", code, msg)
	}

	// Relink an existing chain from the list.
	other := seedExternal(t, e, 7, "office", "socks5://u:p@8.8.4.4:1080#office")
	code, msg, _ = callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": pasted.ForwardID, "name": "pasted", "tunnelId": 20, "targetMode": "chain", "chainExternalId": other})
	if code != 0 {
		t.Fatalf("relink: %d %s", code, msg)
	}
	e.h.chains.wg.Wait()
	after, _ := e.r.GetForwardChain(pasted.ID)
	if after == nil || after.UpstreamHost != "8.8.4.4" || after.UpstreamPort != 1080 {
		t.Fatalf("relinked chain %+v", after)
	}

	// A normal create on a 线路机 tunnel with externalNodeId is refused.
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, map[string]interface{}{"tunnelId": 10, "name": "x", "externalNodeId": extID}); code == 0 || !strings.Contains(msg, "线路机") {
		t.Fatalf("plain external on 线路机: %d %s", code, msg)
	}
}

func TestExternalTargetForUpdate(t *testing.T) {
	for _, c := range []struct {
		req       map[string]interface{}
		current   int64
		old, next string
		landing   int64
		want      int64
	}{
		{map[string]interface{}{}, 5, "a:1", "a:1", 0, 5},                      // untouched
		{map[string]interface{}{}, 5, "a:1", "b:2", 0, 0},                      // typed a new target
		{map[string]interface{}{"externalNodeId": 0}, 5, "a:1", "a:1", 0, 0},   // cleared
		{map[string]interface{}{"externalNodeId": 9}, 5, "a:1", "c:3", 0, 9},   // relinked
		{map[string]interface{}{"externalNodeId": 9}, 5, "a:1", "c:3", 4, 0},   // landing wins
		{map[string]interface{}{"externalNodeId": nil}, 5, "a:1", "a:1", 0, 5}, // null = absent
		{map[string]interface{}{}, 0, "a:1", "b:2", 0, 0},                      // plain stays plain
	} {
		if got := externalTargetForUpdate(c.req, c.current, c.old, c.next, c.landing); got != c.want {
			t.Fatalf("%+v → %d, want %d", c, got, c.want)
		}
	}
}

func TestNormalizeForwardImportExternals(t *testing.T) {
	raw := func() map[string]interface{} {
		return map[string]interface{}{"forward": []interface{}{map[string]interface{}{"id": 1, "external_node_id": 7}, map[string]interface{}{"id": 2}}}
	}
	core := raw()
	normalizeForwardImportExternals(core, []string{"forward", "forward_port"})
	rows := core["forward"].([]interface{})
	if rows[0].(map[string]interface{})["external_node_id"] != 0 {
		t.Fatalf("core import kept the link: %v", rows)
	}
	if _, ok := rows[1].(map[string]interface{})["external_node_id"]; ok {
		t.Fatal("added a column that was not there")
	}
	full := raw()
	normalizeForwardImportExternals(full, []string{"forward", "landing_external_node"})
	if full["forward"].([]interface{})[0].(map[string]interface{})["external_node_id"] != 7 {
		t.Fatal("full import dropped the link")
	}
}
