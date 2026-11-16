//go:build leikwanlab && linux

package socket

import (
	"context"
	"os"
	"time"
)

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
		if os.Getenv("FLVX_LEIKWAN_LAB_WINDOW") == "1" {
			m.now = func() time.Time {
				now := time.Now().UTC()
				return time.Date(now.Year(), now.Month(), now.Day(), 18, 30, now.Second(), 0, time.UTC) // 02:30 TW
			}
		}
	}
}
