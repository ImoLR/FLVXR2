package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const sourceURL = "https://ftp.apnic.net/stats/apnic/delegated-apnic-latest"

func main() {
	output := flag.String("output", "data/cn_cidrs.txt", "generated CIDR file")
	flag.Parse()

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(sourceURL)
	if err != nil {
		fatalf("download APNIC data: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		fatalf("download APNIC data: %s", response.Status)
	}

	prefixes, serial, err := parseDelegated(response.Body)
	if err != nil {
		fatalf("parse APNIC data: %v", err)
	}
	if len(prefixes) == 0 {
		fatalf("parse APNIC data: no CN prefixes found")
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if comparison := prefixes[i].Addr().Compare(prefixes[j].Addr()); comparison != 0 {
			return comparison < 0
		}
		return prefixes[i].Bits() < prefixes[j].Bits()
	})

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}
	file, err := os.Create(*output)
	if err != nil {
		fatalf("create output: %v", err)
	}
	writer := bufio.NewWriter(file)
	_, _ = fmt.Fprintf(writer, "# Generated from %s\n", sourceURL)
	_, _ = fmt.Fprintf(writer, "# APNIC serial: %s; country: CN; address families: IPv4, IPv6\n", serial)
	for _, prefix := range prefixes {
		_, _ = fmt.Fprintln(writer, prefix)
	}
	if err := writer.Flush(); err != nil {
		fatalf("write output: %v", err)
	}
	if err := file.Close(); err != nil {
		fatalf("close output: %v", err)
	}
}

func parseDelegated(reader io.Reader) ([]netip.Prefix, string, error) {
	prefixes := make([]netip.Prefix, 0, 10000)
	serial := "unknown"
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) >= 3 && fields[0] == "2" && fields[1] == "apnic" {
			serial = fields[2]
		}
		if len(fields) < 7 || fields[1] != "CN" || (fields[2] != "ipv4" && fields[2] != "ipv6") {
			continue
		}
		if fields[2] == "ipv6" {
			prefixLength, err := strconv.Atoi(fields[4])
			if err != nil {
				return nil, serial, fmt.Errorf("invalid IPv6 prefix length in %q: %w", line, err)
			}
			prefix, err := netip.ParsePrefix(fmt.Sprintf("%s/%d", fields[3], prefixLength))
			if err != nil {
				return nil, serial, fmt.Errorf("invalid IPv6 row %q: %w", line, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}

		start, err := netip.ParseAddr(fields[3])
		if err != nil || !start.Is4() {
			return nil, serial, fmt.Errorf("invalid IPv4 start in %q", line)
		}
		count, err := strconv.ParseUint(fields[4], 10, 64)
		if err != nil || count == 0 || count > 1<<32 {
			return nil, serial, fmt.Errorf("invalid IPv4 count in %q", line)
		}
		prefixes = append(prefixes, ipv4RangeToPrefixes(start, count)...)
	}
	if err := scanner.Err(); err != nil {
		return nil, serial, err
	}
	return prefixes, serial, nil
}

func ipv4RangeToPrefixes(start netip.Addr, count uint64) []netip.Prefix {
	value := uint64(binary.BigEndian.Uint32(start.AsSlice()))
	prefixes := make([]netip.Prefix, 0, 1)
	for count > 0 {
		alignmentPower := 32
		if value != 0 {
			alignmentPower = bits.TrailingZeros64(value)
			if alignmentPower > 32 {
				alignmentPower = 32
			}
		}
		countPower := bits.Len64(count) - 1
		blockPower := alignmentPower
		if countPower < blockPower {
			blockPower = countPower
		}
		bytes := [4]byte{}
		binary.BigEndian.PutUint32(bytes[:], uint32(value))
		prefixes = append(prefixes, netip.PrefixFrom(netip.AddrFrom4(bytes), 32-blockPower))
		blockSize := uint64(1) << blockPower
		value += blockSize
		count -= blockSize
	}
	return prefixes
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
