package landing

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Geo data is not part of the pinned Core release. Rules are built by the
// Core's API CLI, which reads these files on the agent side and streams only the
// referenced categories into the running Core, so the Core never loads them.
const GeoDataRelease = "202610182205"

var GeoDataSHA256 = map[string]string{
	"geoip.dat":   "010bfc2e353c5e34e23156004944444ca260bae999e73171bdd7974056557959",
	"geosite.dat": "3691de3874692fc8709686998c7627d030df3910d21f3d68232aaa0f1249dd2d",
}

// Expected sizes allow a disk-space check before any download starts.
var geoDataSize = map[string]int64{"geoip.dat": 16587403, "geosite.dat": 11225745}

// githubMirrors matches install.sh GH_MIRRORS and the panel's prefixes.
var githubMirrors = []string{"https://ghfast.top/", "https://gh-proxy.com/", "https://gcode.hostcentral.cc/", ""}

// downloadVerified fetches one pinned asset through the mirror list. Each source
// gets a 30 s idle timer and a 5 min hard limit; partial files are removed.
func downloadVerified(ctx context.Context, direct, path, expected string, limit int64, mode os.FileMode) error {
	for _, prefix := range githubMirrors {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := downloadOnce(ctx, prefix+direct, path, expected, limit, mode); err == nil {
			return nil
		}
	}
	return fmt.Errorf("pinned download or SHA256 validation failed")
}

func downloadOnce(ctx context.Context, url, path, expected string, limit int64, mode os.FileMode) error {
	sourceCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	timer := time.AfterFunc(30*time.Second, cancel)
	defer timer.Stop()
	req, err := http.NewRequestWithContext(sourceCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	tmp := path + ".download"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	writer := io.MultiWriter(f, h)
	buf := make([]byte, 32768)
	var total int64
	for {
		n, readErr := response.Body.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > limit {
				err = fmt.Errorf("asset too large")
				break
			}
			timer.Reset(30 * time.Second)
			if _, err = writer.Write(buf[:n]); err != nil {
				break
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				err = readErr
			}
			break
		}
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != expected {
		err = fmt.Errorf("checksum mismatch")
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func freeDiskBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// ensureGeoData installs only the requested files. Existing files are trusted
// only after their SHA256 matches; the hash is cached by stat stamp so a steady
// state costs one stat per file and render. Failed downloads back off.
func (m *Manager) ensureGeoData(ctx context.Context, names []string) error {
	var missing []string
	var need int64
	for _, name := range names {
		if m.geoVerified(name) {
			continue
		}
		missing = append(missing, name)
		need += geoDataSize[name]
	}
	if len(missing) == 0 {
		return nil
	}
	// Downloads need the file plus a temporary copy and keep 64 MiB spare.
	if free, err := freeDiskBytes(m.dir); err == nil && free < need+64<<20 {
		return fmt.Errorf("节点磁盘空间不足，geo 数据需要约 %d MiB，可用 %d MiB", (need+64<<20)>>20, free>>20)
	}
	for _, name := range missing {
		direct := "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/" + GeoDataRelease + "/" + name
		err := m.retryDownload(ctx, name, func(ctx context.Context) error {
			if err := m.geoDownloadFn()(ctx, direct, filepath.Join(m.dir, name), GeoDataSHA256[name], 64<<20, 0644); err != nil {
				return fmt.Errorf("geo 数据 %s 下载或校验失败", name)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !m.geoVerified(name) {
			return fmt.Errorf("geo 数据 %s 校验失败", name)
		}
	}
	return nil
}

func (m *Manager) geoVerified(name string) bool {
	return m.verifiedFile(filepath.Join(m.dir, name), GeoDataSHA256[name], geoDataSize[name])
}

// readGeoIP returns the CIDRs of one geoip.dat category, scanning the file
// entry by entry like the Core does so only that category is held in memory.
func readGeoIP(path, code string) ([]netip.Prefix, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	code = strings.ToUpper(code)
	br := bufio.NewReaderSize(f, 64<<10)
	var body []byte
	for {
		tag, err := readVarint(br)
		if err == io.EOF {
			return nil, fmt.Errorf("geoip:%s 不存在", strings.ToLower(code))
		}
		if err != nil {
			return nil, err
		}
		length, err := readVarint(br)
		if err != nil || tag != 0x0a || length > 64<<20 {
			return nil, errors.New("geoip.dat 格式无效")
		}
		if uint64(cap(body)) < length {
			body = make([]byte, length)
		}
		body = body[:length]
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, err
		}
		// Other categories are skipped after reading their code; only the
		// requested one is decoded into prefixes.
		if entryCode, err := geoIPEntryCode(body); err != nil {
			return nil, err
		} else if entryCode != code {
			continue
		}
		_, cidrs, err := parseGeoIPEntry(body)
		return cidrs, err
	}
}

// geoIPEntryCode returns the country_code field of one GeoIP entry.
func geoIPEntryCode(b []byte) (string, error) {
	r := bytes.NewReader(b)
	for r.Len() > 0 {
		key, err := readVarint(r)
		if err != nil {
			return "", err
		}
		switch key & 7 {
		case 0:
			if _, err := readVarint(r); err != nil {
				return "", err
			}
		case 2:
			n, err := readVarint(r)
			if err != nil || n > uint64(r.Len()) {
				return "", errors.New("geoip.dat 格式无效")
			}
			if key>>3 == 1 {
				v := make([]byte, n)
				_, _ = io.ReadFull(r, v)
				return strings.ToUpper(string(v)), nil
			}
			if _, err := r.Seek(int64(n), io.SeekCurrent); err != nil {
				return "", err
			}
		default:
			return "", errors.New("geoip.dat 格式无效")
		}
	}
	return "", nil
}

func readVarint(r io.ByteReader) (uint64, error) {
	var x uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := r.ReadByte()
		if err != nil {
			if shift > 0 && err == io.EOF {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
		x |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return x, nil
		}
	}
	return 0, errors.New("varint overflow")
}

// parseGeoIPEntry decodes GeoIP{country_code=1, repeated CIDR cidr=2{ip=1, prefix=2}}.
func parseGeoIPEntry(b []byte) (string, []netip.Prefix, error) {
	r := bytes.NewReader(b)
	code := ""
	var out []netip.Prefix
	for r.Len() > 0 {
		key, err := readVarint(r)
		if err != nil {
			return "", nil, err
		}
		field, wire := key>>3, key&7
		switch wire {
		case 0:
			if _, err := readVarint(r); err != nil {
				return "", nil, err
			}
		case 2:
			n, err := readVarint(r)
			if err != nil || n > uint64(r.Len()) {
				return "", nil, errors.New("geoip.dat 格式无效")
			}
			v := make([]byte, n)
			_, _ = io.ReadFull(r, v)
			switch field {
			case 1:
				code = string(v)
			case 2:
				p, err := parseGeoCIDR(v)
				if err != nil {
					return "", nil, err
				}
				out = append(out, p)
			}
		default:
			return "", nil, errors.New("geoip.dat 格式无效")
		}
	}
	return strings.ToUpper(code), out, nil
}

func parseGeoCIDR(b []byte) (netip.Prefix, error) {
	r := bytes.NewReader(b)
	var ip []byte
	prefix := -1
	for r.Len() > 0 {
		key, err := readVarint(r)
		if err != nil {
			return netip.Prefix{}, err
		}
		switch key {
		case 0x0a:
			n, err := readVarint(r)
			if err != nil || n > uint64(r.Len()) {
				return netip.Prefix{}, errors.New("geoip.dat CIDR 无效")
			}
			ip = make([]byte, n)
			_, _ = io.ReadFull(r, ip)
		case 0x10:
			v, err := readVarint(r)
			if err != nil {
				return netip.Prefix{}, err
			}
			prefix = int(v)
		default:
			return netip.Prefix{}, errors.New("geoip.dat CIDR 无效")
		}
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok || prefix < 0 || prefix > addr.BitLen() {
		return netip.Prefix{}, errors.New("geoip.dat CIDR 无效")
	}
	return netip.PrefixFrom(addr.Unmap(), prefix).Masked(), nil
}
