package socket

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestVerifySHA256Checksum(t *testing.T) {
	sum := sha256.Sum256([]byte("agent-binary"))
	actual := hex.EncodeToString(sum[:])

	if err := verifySHA256Checksum([]byte(actual+"  gost-amd64\n"), actual); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
	if err := verifySHA256Checksum([]byte(strings.Repeat("0", 64)+"  gost-amd64\n"), actual); err == nil {
		t.Fatal("mismatched checksum accepted")
	}
	if err := verifySHA256Checksum([]byte("not-a-sha256"), actual); err == nil {
		t.Fatal("malformed checksum accepted")
	}
	if err := verifySHA256Checksum(nil, actual); err == nil {
		t.Fatal("empty checksum accepted")
	}
}
