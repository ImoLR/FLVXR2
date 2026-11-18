package repo

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// Plan 280: 利群转发数 (user.leikwan_num) and 总转发数 (user.num) are two
// separate allowances; the shared 0 / 99999 sentinel means 不限.
func TestLeiKwanNumSplitFromForwardNum(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			now := time.Now().UnixMilli()
			future := now + 86400000
			mkUser := func(name string, num int) int64 {
				t.Helper()
				id, err := r.CreateUser(name, "x", 1, future, 100, 1, num, 0, 0, 1, now, 0, 0, 0, nil)
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			a, b := mkUser("lk_a", 1), mkUser("lk_b", 1)
			// A new user starts at the default 3.
			var users []model.User
			if err := r.db.Where("id IN ?", []int64{a, b}).Find(&users).Error; err != nil || len(users) != 2 {
				t.Fatalf("users: %v %d", err, len(users))
			}
			if users[0].LeiKwanNum != 3 || users[1].LeiKwanNum != 3 {
				t.Fatalf("default leikwan_num: %d %d", users[0].LeiKwanNum, users[1].LeiKwanNum)
			}
			fwd := func(uid int64, name string) {
				t.Helper()
				if err := r.db.Create(&model.Forward{UserID: uid, UserName: "u", Name: name, TunnelID: 1, RemoteAddr: "1.1.1.1:1", Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			lkSeq := int64(9000)
			lk := func(uid int64) {
				t.Helper()
				lkSeq++
				if err := r.db.Create(&model.LeiKwanRule{UserID: uid, LineID: 1, ForwardID: lkSeq, Name: "lk", Status: 1}).Error; err != nil {
					t.Fatal(err)
				}
			}

			// User a: 1 normal forward fills 总转发数 1 …
			fwd(a, "normal")
			if err := r.CheckUserRuleLimit(a, 1); !IsRuleLimitError(err) || err.Error() != "已达到转发数上限（1 条），请删除不用的转发后再添加" {
				t.Fatalf("normal at limit: %v", err)
			}
			// … but 利群转发 is its own count: 3 fit, the 4th is refused.
			for i := 0; i < 3; i++ {
				if err := r.CheckLeiKwanRuleLimit(a, now, true); err != nil {
					t.Fatalf("利群 %d of 3 refused: %v", i+1, err)
				}
				lk(a)
			}
			err := r.CheckLeiKwanRuleLimit(a, now, true)
			if err == nil || err.Error() != "已达到利群转发数上限（3 条），请删除不用的利群转发后再添加" {
				t.Fatalf("利群 at limit: %v", err)
			}
			// Admin actor is exempt from the count.
			if err := r.CheckLeiKwanRuleLimit(a, now, false); err != nil {
				t.Fatalf("admin not exempt: %v", err)
			}

			// User b: 利群 rows never block a normal forward.
			lk(b)
			lk(b)
			if err := r.CheckUserRuleLimit(b, 1); err != nil {
				t.Fatalf("利群 rows counted against 总转发数: %v", err)
			}
			if c, err := countUserRules(r.db, b); err != nil || c != 0 {
				t.Fatalf("countUserRules includes 利群: %d %v", c, err)
			}

			// Unlimited sentinel, same as num: 0 and ≥ 99999. An explicit 0
			// must be stored (the column DEFAULT 3 must not swallow it).
			for _, n := range []int{0, 99999} {
				if err := r.SetUserLeiKwanNum(a, n); err != nil {
					t.Fatal(err)
				}
				limit, used, err := r.UserLeiKwanRuleLimit(a)
				if err != nil || limit != n || used != 3 {
					t.Fatalf("limit view %d: %d %d %v", n, limit, used, err)
				}
				if err := r.CheckLeiKwanRuleLimit(a, now, true); err != nil {
					t.Fatalf("leikwan_num %d not unlimited: %v", n, err)
				}
			}
			// Lowering below the current count keeps the rows, refuses new ones.
			if err := r.SetUserLeiKwanNum(a, 1); err != nil {
				t.Fatal(err)
			}
			if err := r.CheckLeiKwanRuleLimit(a, now, true); err == nil || !strings.Contains(err.Error(), "（1 条）") {
				t.Fatalf("over limit after lowering: %v", err)
			}
			var kept int64
			r.db.Model(&model.LeiKwanRule{}).Where("user_id = ?", a).Count(&kept)
			if kept != 3 {
				t.Fatalf("existing 利群 rows touched: %d", kept)
			}

			// Expiry still applies to everyone (admin actor included).
			if err := r.db.Model(&model.User{}).Where("id = ?", b).Update("exp_time", now-1000).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.CheckLeiKwanRuleLimit(b, now, false); err == nil || err.Error() != "账号已到期，不能添加利群转发" {
				t.Fatalf("expired: %v", err)
			}
		})
	}
}

// Plan 280: an older panel's user table (no leikwan_num) gets 3 for every
// existing row on migrate; a second start changes nothing.
func TestLeiKwanNumMigrationDefault(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			now := time.Now().UnixMilli()
			for i := 1; i <= 3; i++ {
				if _, err := r.CreateUser(fmt.Sprintf("m%d", i), "x", 1, now+1, 100, 1, 10, 0, 0, 1, now, 0, 0, 0, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.db.Migrator().DropColumn(&model.User{}, "leikwan_num"); err != nil {
				t.Fatal(err)
			}
			if r.db.Migrator().HasColumn(&model.User{}, "leikwan_num") {
				t.Fatal("column not dropped")
			}
			for round := 0; round < 2; round++ {
				if err := autoMigrateAll(r.db); err != nil {
					t.Fatal(err)
				}
				var nums []int
				if err := r.db.Model(&model.User{}).Order("id").Pluck("leikwan_num", &nums).Error; err != nil {
					t.Fatal(err)
				}
				// orphanTestRepo may seed an admin row; every row must be 3.
				if len(nums) < 3 {
					t.Fatalf("round %d: rows %v", round, nums)
				}
				for _, n := range nums {
					if n != 3 {
						t.Fatalf("round %d: leikwan_num %v", round, nums)
					}
				}
			}
		})
	}
}
