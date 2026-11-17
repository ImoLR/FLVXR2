//go:build leikwanlab && linux

package socket

import (
	"context"
	"net"
	"os"
	"strings"
	"time"
)

type labTimeout struct{}

func (labTimeout) Error() string   { return "i/o timeout" }
func (labTimeout) Timeout() bool   { return true }
func (labTimeout) Temporary() bool { return true }

// Lab only: FLVX_LEIKWAN_HOST_ROOT points the 利群主机 module at a fixture
// copy; commands (nft, systemctl) are logged, never run; the clock can be
// shifted into the 02:00–06:00 TW window with FLVX_LEIKWAN_LAB_WINDOW=1.
func init() {
	lkLabHook = func(m *lkHostManager) {
		root := os.Getenv("FLVX_LEIKWAN_HOST_ROOT")
		if root == "" {
			return
		}
		m.root = root
		m.run = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
			f, _ := os.OpenFile(root+"/lab-commands.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if f != nil {
				f.WriteString(time.Now().Format(time.RFC3339) + " " + name)
				for _, a := range args {
					f.WriteString(" " + a)
				}
				f.WriteString("\n")
				if stdin != nil {
					f.Write(stdin)
				}
				f.Close()
			}
			return nil, os.ErrNotExist
		}
		// Plan 272: lab handshakes take a fixed time per ingress (no real dials);
		// an endpoint ending in ":1" times out.
		lkDiagDial = func(ctx context.Context, endpoint string, timeout time.Duration) (time.Duration, error) {
			if strings.HasSuffix(endpoint, ":1") {
				time.Sleep(50 * time.Millisecond)
				return 0, &net.OpError{Op: "dial", Net: "tcp", Err: labTimeout{}}
			}
			ms := 4
			for _, c := range endpoint {
				ms += int(c) % 7
			}
			ms = 4 + ms%28
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return time.Duration(ms) * time.Millisecond, nil
		}
		if os.Getenv("FLVX_LEIKWAN_LAB_WINDOW") == "1" {
			m.now = func() time.Time {
				now := time.Now().UTC()
				return time.Date(now.Year(), now.Month(), now.Day(), 18, 30, now.Second(), 0, time.UTC) // 02:30 TW
			}
		}
	}
}
