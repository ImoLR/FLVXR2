package security

import (
	"strings"
	"testing"
)

func TestPasswordHashAndLegacyVerify(t *testing.T) {
	h, err := HashPassword("secret-1")
	if err != nil || !IsBcryptHash(h) || !strings.HasPrefix(h, "$2a$10$") {
		t.Fatalf("hash %q err %v", h, err)
	}
	if ok, legacy := VerifyPassword(h, "secret-1"); !ok || legacy {
		t.Fatal("bcrypt verify")
	}
	if ok, _ := VerifyPassword(h, "secret-2"); ok {
		t.Fatal("bcrypt accepted wrong password")
	}
	md5 := MD5("old")
	if ok, legacy := VerifyPassword(md5, "old"); !ok || !legacy {
		t.Fatal("legacy md5 verify")
	}
	if ok, _ := VerifyPassword(strings.ToUpper(md5), "old"); !ok {
		t.Fatal("upper-case md5 hex")
	}
	if ok, _ := VerifyPassword(md5, "older"); ok {
		t.Fatal("md5 accepted wrong password")
	}
	if ok, _ := VerifyPassword("", ""); ok {
		t.Fatal("empty hash accepted")
	}
	long := strings.Repeat("p", 100)
	hl, err := HashPassword(long)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword(hl, long); !ok {
		t.Fatal("long password")
	}
	if ok, _ := VerifyPassword(hl, long[:72]); ok {
		t.Fatal("72-byte prefix of a long password accepted")
	}
}
