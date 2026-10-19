package landingegress

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRegisterWarpLive registers one real free WARP device with Cloudflare's
// client API and deletes it again. Opt-in only (FLVX_WARP_LIVE=1): CI and the
// default test run never touch the network.
func TestRegisterWarpLive(t *testing.T) {
	if os.Getenv("FLVX_WARP_LIVE") != "1" {
		t.Skip("set FLVX_WARP_LIVE=1 to call the real Cloudflare WARP API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	acct, err, licenseErr := RegisterWarp(ctx, nil, "")
	if err != nil || licenseErr != nil {
		t.Fatalf("register: %v / %v", err, licenseErr)
	}
	defer func() {
		if err := DeleteWarp(context.Background(), nil, acct.DeviceID, acct.Token); err != nil {
			t.Errorf("delete device: %v", err)
		}
	}()
	if !decodeKey(acct.PrivateKey, false, 32) || !decodeKey(acct.PeerPublicKey, false, 32) {
		t.Fatalf("keys: %+v", acct)
	}
	if acct.AddressV4 == "" || acct.AddressV6 == "" || len(acct.Reserved) != 3 {
		t.Fatalf("account lacks addresses or reserved bytes: v4=%q v6=%q reserved=%v", acct.AddressV4, acct.AddressV6, acct.Reserved)
	}
	t.Logf("WARP device ok: type=%s v4=%s v6=%s endpoint_v4=%s endpoint_v6=%s reserved=%v", acct.AccountType, acct.AddressV4, acct.AddressV6, acct.EndpointV4, acct.EndpointV6, acct.Reserved)
}
