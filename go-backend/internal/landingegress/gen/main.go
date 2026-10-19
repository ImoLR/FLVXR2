// Command gen writes data/geo_codes.txt from the pinned geo data release used
// by landing Cores (Loyalsoldier/v2ray-rules-dat). Each line is
// "<kind> <code> <entry-bytes> <items>"; entry bytes approximate the protobuf
// size the Core API receives for that category.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func varint(r *bufio.Reader) (uint64, error) {
	var x uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		x |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return x, nil
		}
	}
	return 0, fmt.Errorf("varint overflow")
}

func sliceVarint(b []byte, i int) (uint64, int) {
	var x uint64
	for shift := uint(0); i < len(b); shift += 7 {
		c := b[i]
		i++
		x |= uint64(c&0x7f) << shift
		if c < 0x80 {
			break
		}
	}
	return x, i
}

type entry struct {
	code         string
	bytes, items int
}

func scan(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var out []entry
	for {
		if _, err := varint(r); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		n, err := varint(r)
		if err != nil {
			return nil, err
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		e := entry{bytes: int(n)}
		for i := 0; i < len(body); {
			key, j := sliceVarint(body, i)
			i = j
			switch key & 7 {
			case 0:
				_, i = sliceVarint(body, i)
			case 2:
				l, j := sliceVarint(body, i)
				i = j
				if key>>3 == 1 {
					e.code = strings.ToLower(string(body[i : i+int(l)]))
				} else if key>>3 == 2 {
					e.items++
				}
				i += int(l)
			default:
				return nil, fmt.Errorf("unexpected wire type in %s", path)
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].code < out[j].code })
	return out, nil
}

func main() {
	geoip := flag.String("geoip", "geoip.dat", "pinned geoip.dat")
	geosite := flag.String("geosite", "geosite.dat", "pinned geosite.dat")
	output := flag.String("output", "data/geo_codes.txt", "output file")
	flag.Parse()
	var b strings.Builder
	b.WriteString("# Loyalsoldier/v2ray-rules-dat 202610182205: kind code entry-bytes items\n")
	for _, src := range []struct{ kind, path string }{{"ip", *geoip}, {"site", *geosite}} {
		entries, err := scan(src.path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, e := range entries {
			fmt.Fprintf(&b, "%s %s %d %d\n", src.kind, e.code, e.bytes, e.items)
		}
	}
	if err := os.WriteFile(*output, []byte(b.String()), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
