package handler

import (
	"go-backend/internal/store/model"
	"testing"
)

func TestLeiKwanTargetOnlyUsesNodePublicLiteral(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "0.0.0.1", "10.1.2.3", "172.16.2.3", "192.168.1.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "203.0.113.1", "::1", "fd00::1", "fe80::1", "::ffff:127.0.0.1", "2001:db8::1", "224.0.0.1", "255.255.255.255", "example.com", "8.8.8.8:443", "2606:4700:4700::1111%eth0"} {
		if got, err := leiKwanPublicHost(&model.NodeRecord{ServerIP: host}); err == nil {
			t.Errorf("accepted %q => %q", host, got)
		}
	}
	for _, tc := range []struct {
		n    model.NodeRecord
		want string
	}{{model.NodeRecord{ServerIPv4: "8.8.8.8", ServerIPv6: "2606:4700:4700::1111", ServerIP: "1.1.1.1"}, "8.8.8.8"}, {model.NodeRecord{ServerIPv4: "10.0.0.1", ServerIPv6: "2606:4700:4700::1111", ServerIP: "1.1.1.1"}, "2606:4700:4700::1111"}} {
		got, err := leiKwanPublicHost(&tc.n)
		if err != nil || got != tc.want {
			t.Fatalf("got %q %v want %q", got, err, tc.want)
		}
	}
}
