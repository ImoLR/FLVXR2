package ddns

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"time"
)

// AuthoritativeHas asks one authoritative server (ip:53, no recursion)
// whether it already serves name (plan 303 round 3). Asking the zone's own
// servers caches nothing anywhere — unlike a public resolver, which would
// keep a too-early NXDOMAIN for the zone's negative TTL (30 min). One UDP
// query, 2 s timeout.
func AuthoritativeHas(ctx context.Context, server, name string) (bool, error) {
	var id [2]byte
	_, _ = rand.Read(id[:])
	q := []byte{id[0], id[1], 0x00, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(strings.TrimSuffix(strings.ToLower(name), "."), ".") {
		if label == "" || len(label) > 63 {
			return false, errors.New("bad name")
		}
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 5, 0, 1) // CNAME IN
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(q); err != nil {
		return false, err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return false, err
	}
	m := buf[:n]
	if n < 12 || m[0] != id[0] || m[1] != id[1] || m[2]&0x80 == 0 {
		return false, errors.New("bad reply")
	}
	rcode := int(m[3] & 0x0f)
	answers := int(m[6])<<8 | int(m[7])
	switch rcode {
	case 0:
		return answers > 0, nil
	case 3: // NXDOMAIN: not there yet
		return false, nil
	}
	return false, errors.New("rcode " + string(rune('0'+rcode%10)))
}
