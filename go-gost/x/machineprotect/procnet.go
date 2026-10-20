package machineprotect

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Counter reads the whole-machine connection count from /proc/net. It reuses one
// buffer, so a 5 s sample allocates nothing in steady state. Not safe for
// concurrent use (the protection loop owns it).
type Counter struct {
	root string // filesystem root holding proc/net ("/" when empty)
	buf  []byte
}

// NewCounter returns a counter for <root>/proc/net; root "" means the real /proc.
func NewCounter(root string) *Counter {
	return &Counter{root: root, buf: make([]byte, 0, 64<<10)}
}

var errNoProcNet = errors.New("no /proc/net socket tables are readable")

// Read counts the rows of /proc/net/{tcp,tcp6,udp,udp6} exactly like the mmwx
// Helper's readConnections(): rows = lines − 1 (header), an unreadable table counts
// 0, and only all four unreadable is an error. TCP states come from the same pass
// (st column: 01 ESTABLISHED, 06 TIME_WAIT, 08 CLOSE_WAIT, everything else incl.
// LISTEN = other TCP).
func (c *Counter) Read() (Conns, error) {
	var out Conns
	readable := 0
	for _, name := range [...]string{"tcp", "tcp6"} {
		data, ok := c.readTable(name)
		if !ok {
			continue
		}
		readable++
		rows, est, tw, cw := countTCPRows(data)
		out.TCP += rows
		out.Established += est
		out.TimeWait += tw
		out.CloseWait += cw
	}
	for _, name := range [...]string{"udp", "udp6"} {
		data, ok := c.readTable(name)
		if !ok {
			continue
		}
		readable++
		out.UDP += countRows(data)
	}
	if readable == 0 {
		return Conns{}, errNoProcNet
	}
	out.Total = out.TCP + out.UDP
	out.OtherTCP = out.TCP - out.Established - out.TimeWait - out.CloseWait
	return out, nil
}

func (c *Counter) readTable(name string) ([]byte, bool) {
	root := c.root
	if root == "" {
		root = "/"
	}
	f, err := os.Open(filepath.Join(root, "proc", "net", name))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	buf := c.buf[:0]
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := f.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			break
		}
		if err != nil {
			c.buf = buf[:0]
			return nil, false
		}
	}
	c.buf = buf[:0]
	return buf, true
}

// lineCount matches bufio.Scanner line splitting: a final line without a trailing
// newline still counts, an empty file has no lines.
func lineCount(data []byte) int64 {
	var lines int64
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

func countRows(data []byte) int64 {
	lines := lineCount(data)
	if lines == 0 {
		return 0
	}
	return lines - 1
}

// countTCPRows returns the row count (same rule as countRows) and the number of
// ESTABLISHED, TIME_WAIT and CLOSE_WAIT rows. Unparseable rows count as other.
func countTCPRows(data []byte) (rows, est, tw, cw int64) {
	rows = countRows(data)
	first := true
	for start := 0; start < len(data); {
		end := start
		for end < len(data) && data[end] != '\n' {
			end++
		}
		if first {
			first = false
		} else {
			switch tcpState(data[start:end]) {
			case "01":
				est++
			case "06":
				tw++
			case "08":
				cw++
			}
		}
		start = end + 1
	}
	return rows, est, tw, cw
}

// tcpState returns the 4th whitespace-separated field ("st") of a /proc/net/tcp row
// without allocating; "" when the row is too short.
func tcpState(line []byte) string {
	field := 0
	for i := 0; i < len(line); {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		if field == 3 {
			st := line[start:i]
			if len(st) == 2 {
				return upperHex(st)
			}
			return ""
		}
		field++
	}
	return ""
}

// The kernel prints the state as two upper-case hex digits; compare without
// allocating for the three states we break out.
func upperHex(st []byte) string {
	a, b := st[0], st[1]
	if b >= 'a' && b <= 'f' {
		b -= 'a' - 'A'
	}
	switch {
	case a == '0' && b == '1':
		return "01"
	case a == '0' && b == '6':
		return "06"
	case a == '0' && b == '8':
		return "08"
	}
	return "other"
}
