package vlessenc

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"encoding/base64"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestNewKeysRoundTrip(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		k, err := NewKeys()
		if err != nil {
			t.Fatal(err)
		}
		if seen[k.PrivateKey] {
			t.Fatal("duplicate key")
		}
		seen[k.PrivateKey] = true
		priv, err := base64.RawURLEncoding.Strict().DecodeString(k.PrivateKey)
		if err != nil || len(priv) != 32 || priv[0]&7 != 0 || priv[31]&128 != 0 || priv[31]&64 == 0 {
			t.Fatalf("private key not clamped: %x", priv)
		}
		pub, err := PublicFromPrivate(k.PrivateKey)
		if err != nil || pub != k.PublicKey {
			t.Fatalf("public key mismatch %q %q %v", pub, k.PublicKey, err)
		}
		d, e := Decryption("", k.PrivateKey), Encryption("", k.PublicKey)
		if d != "mlkem768x25519plus.random.600s."+k.PrivateKey || e != "mlkem768x25519plus.random.0rtt."+k.PublicKey {
			t.Fatalf("strings: %s %s", d, e)
		}
		if err := ValidateDecryption(d); err != nil {
			t.Fatal(err)
		}
		if err := ValidateEncryption(e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateEncryptionForms(t *testing.T) {
	k, _ := NewKeys()
	// A valid ML-KEM-768 encapsulation key (1184 bytes) from a real seed.
	mlkemKey := mlkemClientKey(t)
	good := []string{
		Encryption("native", k.PublicKey),
		Encryption("xorpub", k.PublicKey),
		"mlkem768x25519plus.random.1rtt." + k.PublicKey,
		"mlkem768x25519plus.native.0rtt." + mlkemKey,
		"mlkem768x25519plus.native.0rtt.100-111-1111.75-0-111.50-0-3333." + k.PublicKey,
		"mlkem768x25519plus.native.0rtt." + k.PublicKey + "." + k.PublicKey,
	}
	for _, s := range good {
		if err := ValidateEncryption(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	bad := []string{
		"", "none", "mlkem768x25519plus.random.0rtt", "mlkem768x25519.random.0rtt." + k.PublicKey,
		"mlkem768x25519plus.weird.0rtt." + k.PublicKey, "mlkem768x25519plus.random.2rtt." + k.PublicKey,
		"mlkem768x25519plus.random.600s." + k.PublicKey, // server ticket on the client side
		"mlkem768x25519plus.random.0rtt." + k.PublicKey + "=", "mlkem768x25519plus.random.0rtt.AAAA",
		"mlkem768x25519plus.random.0rtt." + base64.RawURLEncoding.EncodeToString(make([]byte, 31)),
		"mlkem768x25519plus.random.0rtt." + k.PublicKey + ".100-111-1111", // padding after a key
		"mlkem768x25519plus.random.0rtt.10-111-1111." + k.PublicKey,       // first padding < 100
		"mlkem768x25519plus.random.0rtt.100-1-1111." + k.PublicKey,
		"mlkem768x25519plus.random.0rtt." + strings.Repeat(k.PublicKey+".", 9) + k.PublicKey,
		"mlkem768x25519plus.random.0rtt." + base64.RawURLEncoding.EncodeToString(make([]byte, 64)), // seed is server-side
	}
	for _, s := range bad {
		if err := ValidateEncryption(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []string{"mlkem768x25519plus.random.600-900s." + k.PrivateKey, "mlkem768x25519plus.native.0s." + k.PrivateKey} {
		if err := ValidateDecryption(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{"mlkem768x25519plus.random.0rtt." + k.PrivateKey, "mlkem768x25519plus.random.600." + k.PrivateKey, "mlkem768x25519plus.random.900-600s." + k.PrivateKey} {
		if err := ValidateDecryption(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func mlkemClientKey(t *testing.T) string {
	t.Helper()
	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i)
	}
	dk, err := mlkem.NewDecapsulationKey768(seed)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(dk.EncapsulationKey().Bytes())
}

// TestXrayVLESSEncOutput (FLVX_XRAY_BINARY = an xray / landing Core binary):
// the strings `xray vlessenc` prints pass the validators, and a key pair of
// ours has the same shape as xray's (prefix, ticket, rtt, key lengths).
func TestXrayVLESSEncOutput(t *testing.T) {
	bin := os.Getenv("FLVX_XRAY_BINARY")
	if bin == "" {
		t.Skip("FLVX_XRAY_BINARY not set")
	}
	out, err := exec.Command(bin, "vlessenc").Output()
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`"(decryption|encryption)": "([^"]+)"`)
	m := re.FindAllStringSubmatch(string(out), -1)
	if len(m) != 4 {
		t.Fatalf("unexpected vlessenc output:\n%s", out)
	}
	// X25519 block: decryption private key → our derived public key equals
	// xray's encryption key (same clamping, same encoding).
	d, e := m[0][2], m[1][2]
	if err := ValidateDecryption(d); err != nil {
		t.Fatal(d, err)
	}
	if err := ValidateEncryption(e); err != nil {
		t.Fatal(e, err)
	}
	dp, ep := strings.Split(d, "."), strings.Split(e, ".")
	pub, err := PublicFromPrivate(dp[3])
	if err != nil || pub != ep[3] {
		t.Fatalf("derived %q, xray %q", pub, ep[3])
	}
	if Decryption("native", dp[3]) != d || Encryption("native", pub) != e {
		t.Fatalf("format differs from xray: %s / %s", Decryption("native", dp[3]), Encryption("native", pub))
	}
	// ML-KEM block parses too.
	if err := ValidateDecryption(m[2][2]); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEncryption(m[3][2]); err != nil {
		t.Fatal(err)
	}
	_ = ecdh.X25519()
}
