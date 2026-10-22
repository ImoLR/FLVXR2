package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Password storage (plan 135): new and changed passwords are bcrypt (cost 10).
// Accounts created before keep their unsalted MD5 hex until the next
// successful login, which re-hashes them (VerifyPassword reports legacy=true).

const PasswordCost = 10

// bcrypt reads at most 72 bytes; longer passwords are pre-hashed so they keep
// working (the old MD5 scheme accepted any length).
func bcryptInput(password string) []byte {
	if len(password) <= 72 {
		return []byte(password)
	}
	sum := sha256.Sum256([]byte(password))
	return []byte("s2:" + hex.EncodeToString(sum[:]))
}

// HashPassword returns the bcrypt hash to store for password.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(bcryptInput(password), PasswordCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// IsBcryptHash reports whether stored is a bcrypt hash.
func IsBcryptHash(stored string) bool {
	return strings.HasPrefix(stored, "$2a$") || strings.HasPrefix(stored, "$2b$") || strings.HasPrefix(stored, "$2y$")
}

// VerifyPassword checks password against a stored bcrypt or legacy MD5 hash.
// legacy=true means it matched an MD5 hash that should be upgraded.
func VerifyPassword(stored, password string) (ok bool, legacy bool) {
	if stored == "" {
		return false, false
	}
	if IsBcryptHash(stored) {
		return bcrypt.CompareHashAndPassword([]byte(stored), bcryptInput(password)) == nil, false
	}
	want := MD5(password)
	if len(stored) == len(want) && subtle.ConstantTimeCompare([]byte(strings.ToLower(stored)), []byte(want)) == 1 {
		return true, true
	}
	return false, false
}
