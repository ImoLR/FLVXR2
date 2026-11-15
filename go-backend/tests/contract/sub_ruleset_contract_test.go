package contract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/sublink"
)

// TestSubRuleSetContract: plan 261 「规则集链接」 storage on SQLite and
// PostgreSQL — the new table (auto-migrate), its unique indexes, per-user
// cap, failure counter, rules-less reads, ownership, GC, user delete, and
// the public /s/rs/u/ files + the owner's pool view through the full router.
func TestSubRuleSetContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			for _, u := range []*model.User{
				{ID: 861, User: "ruleset-a", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				{ID: 862, User: "ruleset-b", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
			} {
				if err := r.DB().Create(u).Error; err != nil {
					t.Fatal(err)
				}
			}
			rules := "DOMAIN-SUFFIX,netflix.com\nDOMAIN,fast.com\nIP-CIDR,23.246.0.0/18\n"
			mk := func(uid int64, i int) *model.UserSubRuleSet {
				return &model.UserSubRuleSet{UserID: uid, URLEnc: "sealed", URLHint: "rules.example/…", URLHash: fmt.Sprintf("%064d", i),
					PublicID: fmt.Sprintf("%032x", uid*1000+int64(i)), Rules: rules, RuleCount: 3, CountSuffix: 1, CountDomain: 1, CountIP: 1,
					ContentHash: sublink.LinkHash([]byte(rules)), FetchedAt: now, LastTryAt: now, CreatedTime: now - 3600_000}
			}
			var sets []*model.UserSubRuleSet
			for i := 0; i < model.MaxSubRuleSetsPerUser; i++ {
				s := mk(861, i)
				if err := r.CreateSubRuleSet(s, model.MaxSubRuleSetsPerUser); err != nil {
					t.Fatalf("create %d: %v", i, err)
				}
				sets = append(sets, s)
			}
			if err := r.CreateSubRuleSet(mk(861, 99), model.MaxSubRuleSetsPerUser); !errors.Is(err, repo.ErrSubCap) {
				t.Fatalf("cap: %v", err)
			}
			// One row per user + URL hash; the same hash for another user is fine.
			if err := r.CreateSubRuleSet(mk(862, 0), 0); err != nil {
				t.Fatalf("other user same hash: %v", err)
			}
			dup := mk(862, 0)
			dup.PublicID = strings.Repeat("e", 32)
			if err := r.CreateSubRuleSet(dup, 0); err == nil {
				t.Fatal("duplicate user + url hash accepted")
			}
			a := sets[0]
			if err := r.SaveSubRuleSetFail(a.ID, "打不开：对方返回 HTTP 404", now+1); err != nil {
				t.Fatal(err)
			}
			if err := r.SaveSubRuleSetFail(a.ID, "打不开：对方返回 HTTP 404", now+2); err != nil {
				t.Fatal(err)
			}
			got, err := r.GetSubRuleSet(a.ID)
			if err != nil || got.FailCount != 2 || got.LastError == "" || got.Rules != "" || got.RuleCount != 3 {
				t.Fatalf("after failures %+v %v", got, err)
			}
			if text, err := r.SubRuleSetRules(a.ID); err != nil || text != rules {
				t.Fatalf("rules %q %v", text, err)
			}
			newRules := "DOMAIN-SUFFIX,netflix.com\n"
			if err := r.SaveSubRuleSetOK(a.ID, repo.SubRuleSetContent{Rules: newRules, Total: 1, Suffix: 1, Hash: sublink.LinkHash([]byte(newRules))}, now+3); err != nil {
				t.Fatal(err)
			}
			if got, _ := r.SubRuleSetByPublicID(a.PublicID); got == nil || got.FailCount != 0 || got.LastError != "" || got.RuleCount != 1 || got.Rules != "" {
				t.Fatalf("after ok %+v", got)
			}
			if own, _ := r.SubRuleSetsByIDs(862, []int64{a.ID}); len(own) != 0 {
				t.Fatal("other user loaded the set")
			}

			// A subscription that uses set a; GC drops every other set of 861.
			routing := fmt.Sprintf(`[{"type":"link","name":"奈飞","exit":"direct","ref":%d}]`, a.ID)
			if err := r.DB().Create(&model.UserSubscription{ID: 81, UserID: 861, Name: "看片", Token: "TokRuleSetContract123456", Format: "auto", RuleMode: "cn_direct", Items: `[]`, Routing: &routing, CreatedTime: now}).Error; err != nil {
				t.Fatal(err)
			}
			gone, err := r.GCSubRuleSets(861, now)
			if err != nil || len(gone) != model.MaxSubRuleSetsPerUser-1 {
				t.Fatalf("gc %d %v", len(gone), err)
			}
			if n, _ := r.CountSubRuleSets(861); n != 1 {
				t.Fatalf("left %d", n)
			}

			// Through the router: public files and the owner's pool view.
			h := handler.New(r, subSecret, "test")
			router := httpserver.NewRouter(h, subSecret, r)
			get := func(path string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				return w
			}
			if w := get("/s/rs/u/" + a.PublicID + ".list"); w.Code != 200 || w.Body.String() != newRules || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("list %d %q", w.Code, w.Body.String())
			}
			if w := get("/s/rs/u/" + a.PublicID + ".json"); w.Code != 200 || !strings.Contains(w.Body.String(), `"domain_suffix":["netflix.com"]`) {
				t.Fatalf("json %d %q", w.Code, w.Body.String())
			}
			if w := get("/s/rs/u/" + sets[1].PublicID + ".list"); w.Code != 404 {
				t.Fatalf("gc'd set served: %d", w.Code)
			}
			token, _ := auth.GenerateToken(861, "ruleset-a", 1, subSecret)
			raw, _ := json.Marshal(map[string]any{"id": 81})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sub/pool", bytes.NewReader(raw))
			req.Header.Set("Authorization", token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			var out response.R
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 {
				t.Fatalf("pool %s", w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"routingLinks":{"`+fmt.Sprint(a.ID)+`":{`) || strings.Contains(w.Body.String(), "sealed") || strings.Contains(w.Body.String(), a.PublicID) {
				t.Fatalf("pool view %s", w.Body.String())
			}

			// User delete removes the user's sets.
			if err := r.DeleteUserCascade(861); err != nil {
				t.Fatal(err)
			}
			if n, _ := r.CountSubRuleSets(861); n != 0 {
				t.Fatalf("sets left after user delete: %d", n)
			}
			if n, _ := r.CountSubRuleSets(862); n != 1 {
				t.Fatalf("other user's set touched: %d", n)
			}
		})
	}
}
