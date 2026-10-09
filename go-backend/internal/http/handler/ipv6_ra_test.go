package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
)

func TestIPv6RAAdminListAndNodeSave(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addNode(1, "ra")
	if err := e.r.UpdateNodeIPv6RA(1, "ok", "eth0 accept_ra=2", 1234); err != nil {
		t.Fatal(err)
	}
	request := func(body string, role int, handle func(http.ResponseWriter, *http.Request)) []byte {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: role}))
		w := httptest.NewRecorder()
		handle(w, r)
		var result struct {
			Code int
			Msg  string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Code != 0 {
			t.Fatalf("%s %v", w.Body.String(), err)
		}
		return w.Body.Bytes()
	}
	for _, role := range []int{0, 1} {
		raw := request(`{}`, role, e.h.nodeList)
		for _, key := range []string{"ipv6RaStatus", "ipv6RaDetail", "ipv6RaCheckedAt"} {
			if bytes.Contains(raw, []byte(key)) != (role == 0) {
				t.Fatalf("role=%d field=%s: %s", role, key, raw)
			}
		}
	}
	request(`{"id":1,"name":"edited","serverIp":"127.0.0.1","port":"1000-2000","ipv6RaStatus":"","ipv6RaDetail":"","ipv6RaCheckedAt":0}`, 0, e.h.nodeUpdate)
	var node model.Node
	if err := e.r.DB().First(&node, 1).Error; err != nil {
		t.Fatal(err)
	}
	if node.Name != "edited" || node.IPv6RAStatus != "ok" || node.IPv6RADetail != "eth0 accept_ra=2" || node.IPv6RACheckedAt != 1234 {
		t.Fatalf("save cleared report: %+v", node)
	}
}
