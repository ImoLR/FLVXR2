package socket

// Plan 269 利群主机接管 (step 2). On the 利群入口 machines the agent reads the
// hand-made LeiKwan host files (front warm pool / SS2022 first-packet filter +
// keys / logswitch lines + triggers / first-chunk coalescing / phone backup
// DNAT), reports their meaning plus a secret-free host status to the panel, and
// — only with the per-host 「面板接管」 switch on — rewrites a file when the
// panel's wanted content differs. It also counts the bytes of every 利群 rule
// port (own nft table, conntrack original destination) and blocks ports the
// panel asks for. One goroutine; host checks on a panel push or ≥ 60 s.
//
// This file holds the platform-independent part (the JSON contract with the
// panel, normalization, comparison and rendering); leikwan_host_linux.go reads
// and writes the host.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const leiKwanHostVersion = 1

// File keys (report, desired, drift/pending lists).
const (
	lkFileWarm      = "warm"
	lkFileSSFilter  = "ssfilter"
	lkFileSSKeys    = "sskeys"
	lkFileTriggers  = "triggers"
	lkFileLogswitch = "logswitch"
	lkFileCoalesce  = "coalesce"
	lkFilePhone     = "phone"
)

var lkFileOrder = []string{lkFileWarm, lkFileSSFilter, lkFileSSKeys, lkFileTriggers, lkFileLogswitch, lkFileCoalesce, lkFilePhone}

// ------------------------------------------------------------ host views

// LKTokens: a key=value token file (warm, ssfilter, triggers) as the reading
// program sees it: comments dropped, last value wins, normalized.
type LKTokens struct {
	Path    string            `json:"path,omitempty"`
	Present bool              `json:"present"`
	Tokens  map[string]string `json:"tokens"`
	Ignored []string          `json:"ignored,omitempty"`
}

// LKKeys: the SS2022 key file; only a fingerprint per forward leaves the host.
type LKKeys struct {
	Path    string            `json:"path,omitempty"`
	Present bool              `json:"present"`
	FP      map[string]string `json:"fp"`
	Error   string            `json:"error,omitempty"`
}

// LKKeyEntry is one forward's key entry, as in the front's sskeys.json.
type LKKeyEntry struct {
	Method string   `json:"method"`
	PSK    string   `json:"psk"`
	Users  []string `json:"users,omitempty"`
}

// LKLogswitchRun is what the running logswitch process was started with,
// reported only when it differs from the unit (a restart is pending).
type LKLogswitchRun struct {
	Lines      []string `json:"lines"`
	RecordOnly []string `json:"recordOnly"`
}

// LKLogswitch: LOGSWITCH_LINES + LOGSWITCH_RECORD_ONLY of the unit (+ drop-ins).
type LKLogswitch struct {
	Unit         string          `json:"unit,omitempty"`
	DropIn       string          `json:"dropIn,omitempty"`
	Present      bool            `json:"present"`
	Lines        []string        `json:"lines"`
	RecordOnly   []string        `json:"recordOnly"`
	Running      *LKLogswitchRun `json:"running,omitempty"`
	Build        string          `json:"build,omitempty"`
	TriggersFile string          `json:"triggersFile,omitempty"`
	Hold         string          `json:"hold,omitempty"`
	Grace        string          `json:"grace,omitempty"`
	LeaseSettle  string          `json:"leaseSettle,omitempty"`
	ReturnCheck  string          `json:"returnCheck,omitempty"`
}

// LKValue: a one-value file (coalesce).
type LKValue struct {
	Path    string `json:"path,omitempty"`
	Present bool   `json:"present"`
	Value   string `json:"value"`
}

// LKPhone: the phone backup DNAT file (port → target).
type LKPhone struct {
	Path     string            `json:"path,omitempty"`
	Present  bool              `json:"present"`
	Rules    map[string]string `json:"rules"`
	Comments map[string]string `json:"comments,omitempty"`
}

// LKFiles: the managed files of one host (nil = that file does not apply
// here, e.g. no warm file on a stable front).
type LKFiles struct {
	Warm      *LKTokens    `json:"warm,omitempty"`
	SSFilter  *LKTokens    `json:"ssfilter,omitempty"`
	SSKeys    *LKKeys      `json:"sskeys,omitempty"`
	Triggers  *LKTokens    `json:"triggers,omitempty"`
	Logswitch *LKLogswitch `json:"logswitch,omitempty"`
	Coalesce  *LKValue     `json:"coalesce,omitempty"`
	Phone     *LKPhone     `json:"phone,omitempty"`
}

// LKForward is one LeiKwan forward of the client on this host (public fields only).
type LKForward struct {
	ForwardID  int64  `json:"forwardId"`
	ListenPort int    `json:"listenPort"`
	Target     string `json:"target,omitempty"`
	Transport  string `json:"transport,omitempty"`
	State      string `json:"state,omitempty"`
	Policy     string `json:"policy,omitempty"`
	UDP        string `json:"udp,omitempty"`
	Running    bool   `json:"running"`
}

// LKHostState: secret-free status of the LeiKwan stack on this host.
type LKHostState struct {
	Host          string      `json:"host,omitempty"`
	AgentVersion  string      `json:"agentVersion,omitempty"`
	CoreVersion   string      `json:"coreVersion,omitempty"`
	AgentRunning  bool        `json:"agentRunning"`
	UpdateLock    bool        `json:"updateLock"`
	AutoUpdateOff bool        `json:"autoUpdateOff"`
	SelectionMode string      `json:"selectionMode,omitempty"`
	NativeUDP     string      `json:"nativeUdp,omitempty"`
	FrontChannel  string      `json:"frontChannel,omitempty"`
	FrontBuild    string      `json:"frontBuild,omitempty"`
	FastFailover  string      `json:"fastFailover,omitempty"`
	Logswitch     string      `json:"logswitch,omitempty"`
	Coalesce      string      `json:"coalesce,omitempty"`
	PhonePorts    []int       `json:"phonePorts,omitempty"`
	Forwards      []LKForward `json:"forwards"`
}

// LKManageStatus: what the agent did with the panel's wanted content.
type LKManageStatus struct {
	Manage  bool              `json:"manage"`
	Rev     int64             `json:"rev"`
	Force   int64             `json:"force"`
	Applied map[string]int64  `json:"applied,omitempty"`
	Drift   []string          `json:"drift,omitempty"`
	Pending []string          `json:"pending,omitempty"`
	Errors  map[string]string `json:"errors,omitempty"`
	Written map[string]int64  `json:"written,omitempty"` // file → unix ms of the last write
	Backup  string            `json:"backup,omitempty"`  // newest backup dir
}

// LKTraffic: raw counters of the flvx_leikwan table per port ([up, down] bytes).
type LKTraffic struct {
	Epoch   string              `json:"epoch"`
	Ports   map[string][2]int64 `json:"ports"`
	Blocked []int               `json:"blocked,omitempty"`
	Pending bool                `json:"pending,omitempty"` // table change waits for the window
	Error   string              `json:"error,omitempty"`
}

// LeiKwanHostReport goes to the panel inside the system info, only when
// something changed (≤ 1 / min) or every 5 min.
type LeiKwanHostReport struct {
	V       int            `json:"v"`
	At      int64          `json:"at"`
	State   LKHostState    `json:"state"`
	Files   LKFiles        `json:"files"`
	Manage  LKManageStatus `json:"manage"`
	Traffic *LKTraffic     `json:"traffic,omitempty"`
}

// ------------------------------------------------------------ panel → agent

type LKWantTokens struct {
	Tokens map[string]string `json:"tokens"`
}
type LKWantKeys struct {
	Set    map[string]LKKeyEntry `json:"set,omitempty"`
	Remove []string              `json:"remove,omitempty"`
}
type LKWantLogswitch struct {
	Lines      []string `json:"lines"`
	RecordOnly []string `json:"recordOnly"`
}
type LKWantValue struct {
	Value string `json:"value"`
}
type LKWantPhone struct {
	Rules    map[string]string `json:"rules"`
	Comments map[string]string `json:"comments,omitempty"`
}

// LKDesired: nil fields are not managed (never touched).
type LKDesired struct {
	Warm      *LKWantTokens    `json:"warm,omitempty"`
	SSFilter  *LKWantTokens    `json:"ssfilter,omitempty"`
	SSKeys    *LKWantKeys      `json:"sskeys,omitempty"`
	Triggers  *LKWantTokens    `json:"triggers,omitempty"`
	Logswitch *LKWantLogswitch `json:"logswitch,omitempty"`
	Coalesce  *LKWantValue     `json:"coalesce,omitempty"`
	Phone     *LKWantPhone     `json:"phone,omitempty"`
}

// LeiKwanHostSync is the panel's LeiKwanHostSync command.
type LeiKwanHostSync struct {
	V       int        `json:"v"`
	Manage  bool       `json:"manage"`
	Rev     int64      `json:"rev"`
	Force   int64      `json:"force"`
	Desired *LKDesired `json:"desired,omitempty"`
	Traffic bool       `json:"traffic"`
	Ports   []int      `json:"ports"`
	Block   []int      `json:"block"`
}

// ------------------------------------------------------------ normalization

var (
	lkPoolKey   = regexp.MustCompile(`^(backup:)?[1-9][0-9]*$`)
	lkForwardID = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// lkTokenize splits a token file like the front / logswitch do: '#' comments,
// fields separated by newlines, commas, spaces, tabs; "key=value"; last wins.
func lkTokenize(text string) (map[string]string, []string) {
	out := map[string]string{}
	var ignored []string
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }) {
			k, v, ok := strings.Cut(field, "=")
			if !ok || k == "" {
				ignored = append(ignored, field)
				continue
			}
			out[k] = v
		}
	}
	return out, ignored
}

// lkNormWarm: a pool of 0 is the same as no pool (the stable path).
func lkNormWarm(t map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range t {
		if lkPoolKey.MatchString(k) && v == "0" {
			continue
		}
		out[k] = v
	}
	return out
}

// lkNormSSFilter: "off" is the same as not listed.
func lkNormSSFilter(t map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range t {
		if v == "off" {
			continue
		}
		out[k] = v
	}
	return out
}

// lkNormTriggers: "*:x" is the same as "x".
func lkNormTriggers(t map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range t {
		out[strings.TrimPrefix(k, "*:")] = v
	}
	return out
}

func lkNormValue(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return "off"
	}
	return v
}

// lkSortLines sorts logswitch items "fid:port>host:port" by forward id.
func lkSortLines(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.SplitN(out[i], ":", 2)[0])
		b, _ := strconv.Atoi(strings.SplitN(out[j], ":", 2)[0])
		if a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

func lkSortUnique(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func lkSplitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LKKeyFingerprint: the panel computes the same value for the keys it owns.
func LKKeyFingerprint(e LKKeyEntry) string {
	sum := sha256.Sum256([]byte(e.Method + "|" + e.PSK + "|" + strings.Join(e.Users, ",")))
	return hex.EncodeToString(sum[:8])
}

func lkMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func lkListsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------ comparison

// lkSame reports whether the host already has what the panel wants for file f.
func lkSame(f string, h *LKFiles, d *LKDesired) bool {
	switch f {
	case lkFileWarm:
		return h.Warm != nil && d.Warm != nil && lkMapsEqual(lkNormWarm(h.Warm.Tokens), lkNormWarm(d.Warm.Tokens)) && (h.Warm.Present || len(lkNormWarm(d.Warm.Tokens)) == 0)
	case lkFileSSFilter:
		return h.SSFilter != nil && d.SSFilter != nil && lkMapsEqual(lkNormSSFilter(h.SSFilter.Tokens), lkNormSSFilter(d.SSFilter.Tokens)) && (h.SSFilter.Present || len(lkNormSSFilter(d.SSFilter.Tokens)) == 0)
	case lkFileTriggers:
		return h.Triggers != nil && d.Triggers != nil && lkMapsEqual(lkNormTriggers(h.Triggers.Tokens), lkNormTriggers(d.Triggers.Tokens)) && (h.Triggers.Present || len(d.Triggers.Tokens) == 0)
	case lkFileSSKeys:
		if h.SSKeys == nil || d.SSKeys == nil {
			return false
		}
		for fid, e := range d.SSKeys.Set {
			if h.SSKeys.FP[fid] != LKKeyFingerprint(e) {
				return false
			}
		}
		for _, fid := range d.SSKeys.Remove {
			if _, ok := h.SSKeys.FP[fid]; ok {
				return false
			}
		}
		return true
	case lkFileLogswitch:
		return h.Logswitch != nil && d.Logswitch != nil && h.Logswitch.Present &&
			lkListsEqual(lkSortLines(h.Logswitch.Lines), lkSortLines(d.Logswitch.Lines)) &&
			lkListsEqual(lkSortUnique(h.Logswitch.RecordOnly), lkSortUnique(d.Logswitch.RecordOnly))
	case lkFileCoalesce:
		return h.Coalesce != nil && d.Coalesce != nil && lkNormValue(h.Coalesce.Value) == lkNormValue(d.Coalesce.Value) && (h.Coalesce.Present || lkNormValue(d.Coalesce.Value) == "off")
	case lkFilePhone:
		return h.Phone != nil && d.Phone != nil && lkMapsEqual(h.Phone.Rules, d.Phone.Rules) && (h.Phone.Present || len(d.Phone.Rules) == 0)
	}
	return false
}

// lkWanted reports whether the panel manages file f.
func lkWanted(f string, d *LKDesired) bool {
	if d == nil {
		return false
	}
	switch f {
	case lkFileWarm:
		return d.Warm != nil
	case lkFileSSFilter:
		return d.SSFilter != nil
	case lkFileSSKeys:
		return d.SSKeys != nil
	case lkFileTriggers:
		return d.Triggers != nil
	case lkFileLogswitch:
		return d.Logswitch != nil
	case lkFileCoalesce:
		return d.Coalesce != nil
	case lkFilePhone:
		return d.Phone != nil
	}
	return false
}

// lkWindowed: files whose apply restarts something or changes nft rules.
func lkWindowed(f string) bool { return f == lkFileLogswitch || f == lkFilePhone }

// lkInWindow: 02:00–06:00 Taiwan time (UTC+8), except 03:45–04:20 (the boil
// IP change). Host time zones differ, so the offset is fixed.
func lkInWindow(now time.Time) bool {
	t := now.UTC().Add(8 * time.Hour)
	m := t.Hour()*60 + t.Minute()
	if m < 120 || m >= 360 {
		return false
	}
	return m < 225 || m >= 260
}

// ------------------------------------------------------------ rendering

const lkHeader = "# 由 FLVX 面板生成（利群主机接管 rev %d，%s）。手改会被面板标成漂移，不会被悄悄改回。\n"

func lkHeaderLine(rev int64, now time.Time) string {
	return fmt.Sprintf(lkHeader, rev, now.UTC().Add(8*time.Hour).Format("2006-01-02 15:04 台湾"))
}

func lkSortedForwardKeys(t map[string]string, pred func(string) (int, bool)) []string {
	type kv struct {
		k string
		n int
	}
	var out []kv
	for k := range t {
		if n, ok := pred(k); ok {
			out = append(out, kv{k, n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n < out[j].n
		}
		return out[i].k < out[j].k
	})
	keys := make([]string, len(out))
	for i, x := range out {
		keys[i] = x.k
	}
	return keys
}

func lkForwardNum(s string) (int, bool) {
	if !lkForwardID.MatchString(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// lkRenderWarm: main pools, backup pools, then the other keys (standby, pace,
// burst headroom…) one per line; "burst=" last.
func lkRenderWarm(t map[string]string, rev int64, now time.Time) string {
	t = lkNormWarm(t)
	var b strings.Builder
	b.WriteString(lkHeaderLine(rev, now))
	done := map[string]bool{}
	write := func(k string) {
		if done[k] {
			return
		}
		done[k] = true
		b.WriteString(k + "=" + t[k] + "\n")
	}
	for _, k := range lkSortedForwardKeys(t, lkForwardNum) {
		write(k)
	}
	for _, k := range lkSortedForwardKeys(t, func(k string) (int, bool) {
		rest, ok := strings.CutPrefix(k, "backup:")
		if !ok {
			return 0, false
		}
		return lkForwardNum(rest)
	}) {
		write(k)
	}
	for _, k := range []string{"backup_standby", "pace_ms", "backup_pace_ms"} {
		if _, ok := t[k]; ok {
			write(k)
		}
	}
	rest := make([]string, 0, len(t))
	for k := range t {
		if !done[k] && k != "burst" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		write(k)
	}
	if _, ok := t["burst"]; ok {
		write("burst")
	}
	return b.String()
}

func lkRenderSSFilter(t map[string]string, rev int64, now time.Time) string {
	var b strings.Builder
	b.WriteString(lkHeaderLine(rev, now))
	t = lkNormSSFilter(t)
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		c, _ := strconv.Atoi(keys[j])
		if a != c {
			return a < c
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		b.WriteString(k + "=" + t[k] + "\n")
	}
	return b.String()
}

// lkRenderTriggers: global items first (known order), then per forward.
func lkRenderTriggers(t map[string]string, rev int64, now time.Time) string {
	t = lkNormTriggers(t)
	var b strings.Builder
	b.WriteString(lkHeaderLine(rev, now))
	b.WriteString("# 日志快切触发条件（logswitch 每秒重读，不用重启）：TRIGGER=switch|record|off，FORWARD:TRIGGER=…；return_check=on|off\n")
	order := map[string]int{"timeout": 0, "warm_death": 1, "io_error": 2, "return_check": 3}
	var global, per []string
	for k := range t {
		if strings.Contains(k, ":") {
			per = append(per, k)
		} else {
			global = append(global, k)
		}
	}
	sort.Slice(global, func(i, j int) bool {
		a, okA := order[global[i]]
		c, okC := order[global[j]]
		switch {
		case okA && okC:
			return a < c
		case okA != okC:
			return okA
		}
		return global[i] < global[j]
	})
	sort.Slice(per, func(i, j int) bool {
		fa, ka, _ := strings.Cut(per[i], ":")
		fb, kb, _ := strings.Cut(per[j], ":")
		a, _ := strconv.Atoi(fa)
		c, _ := strconv.Atoi(fb)
		if a != c {
			return a < c
		}
		return ka < kb
	})
	for _, k := range append(global, per...) {
		b.WriteString(k + "=" + t[k] + "\n")
	}
	return b.String()
}

func lkRenderCoalesce(v string) string { return lkNormValue(v) + "\n" }

// lkRenderLogswitchDropIn: the panel's LOGSWITCH_LINES / RECORD_ONLY override
// the unit's (drop-ins are read after the unit, last assignment wins).
func lkRenderLogswitchDropIn(d *LKWantLogswitch, rev int64, now time.Time) string {
	var b strings.Builder
	b.WriteString(lkHeaderLine(rev, now))
	b.WriteString("# 改了要 daemon-reload + 重启 leikwan-logswitch；agent 只在台湾 02:00–06:00（不含 03:45–04:20）做。\n")
	b.WriteString("[Service]\n")
	b.WriteString("Environment=LOGSWITCH_LINES=" + strings.Join(lkSortLines(d.Lines), ",") + "\n")
	b.WriteString("Environment=LOGSWITCH_RECORD_ONLY=" + strings.Join(lkSortUnique(d.RecordOnly), ",") + "\n")
	return b.String()
}

// lkRenderPhone: same layout as the hand-made file (own table, TCP only).
func lkRenderPhone(d *LKWantPhone, rev int64, now time.Time) string {
	ports := make([]int, 0, len(d.Rules))
	for p := range d.Rules {
		if n, err := strconv.Atoi(p); err == nil {
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)
	var b strings.Builder
	b.WriteString(lkHeaderLine(rev, now))
	b.WriteString("# 利群手机备线端口（TCP），由 leikwan-phone-backup.service 加载；只动自己的表。\n")
	b.WriteString("table ip leikwan_phone_backup\ndelete table ip leikwan_phone_backup\ntable ip leikwan_phone_backup {\n")
	b.WriteString("  chain pre {\n    type nat hook prerouting priority dstnat - 4; policy accept;\n")
	targets := map[string][]int{}
	for _, p := range ports {
		target := d.Rules[strconv.Itoa(p)]
		line := fmt.Sprintf("    tcp dport %d counter dnat to %s", p, target)
		if c := strings.TrimSpace(d.Comments[strconv.Itoa(p)]); c != "" {
			line += " comment \"" + strings.ReplaceAll(c, "\"", "'") + "\""
		}
		b.WriteString(line + "\n")
		host, port, ok := strings.Cut(target, ":")
		if n, err := strconv.Atoi(port); ok && err == nil {
			targets[host] = append(targets[host], n)
		}
	}
	b.WriteString("  }\n  chain post {\n    type nat hook postrouting priority srcnat - 4; policy accept;\n")
	hosts := make([]string, 0, len(targets))
	for h := range targets {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		seen := map[int]bool{}
		var list []string
		for _, p := range targets[h] {
			if !seen[p] {
				seen[p] = true
				list = append(list, strconv.Itoa(p))
			}
		}
		if len(list) == 1 {
			b.WriteString(fmt.Sprintf("    ip daddr %s tcp dport %s ct status dnat counter masquerade\n", h, list[0]))
		} else {
			b.WriteString(fmt.Sprintf("    ip daddr %s tcp dport { %s } ct status dnat counter masquerade\n", h, strings.Join(list, ", ")))
		}
	}
	b.WriteString("  }\n}\n")
	return b.String()
}

// lkRenderSSKeys applies the panel's set/remove to the host file's entries;
// entries the panel does not name are kept byte for byte.
func lkRenderSSKeys(old []byte, d *LKWantKeys) ([]byte, error) {
	entries := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(old))) > 0 {
		if err := json.Unmarshal(old, &entries); err != nil {
			return nil, fmt.Errorf("sskeys.json 不是有效 JSON")
		}
	}
	for _, fid := range d.Remove {
		delete(entries, fid)
	}
	for fid, e := range d.Set {
		if !lkForwardID.MatchString(fid) {
			return nil, fmt.Errorf("bad forward id")
		}
		raw, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		entries[fid] = raw
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		c, _ := strconv.Atoi(keys[j])
		return a < c
	})
	var b strings.Builder
	b.WriteString("{\n")
	for i, k := range keys {
		sep := ","
		if i == len(keys)-1 {
			sep = ""
		}
		b.WriteString(" " + strconv.Quote(k) + ": " + string(entries[k]) + sep + "\n")
	}
	b.WriteString("}\n")
	return []byte(b.String()), nil
}

// ------------------------------------------------------------ parsing helpers

// lkParseKeys returns forward → fingerprint of a sskeys.json body.
func lkParseKeys(data []byte) (map[string]string, string) {
	fp := map[string]string{}
	var entries map[string]LKKeyEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fp, "invalid_json"
	}
	for fid, e := range entries {
		if !lkForwardID.MatchString(fid) {
			continue
		}
		fp[fid] = LKKeyFingerprint(e)
	}
	return fp, ""
}

var lkPhoneRule = regexp.MustCompile(`tcp dport ([0-9]+) .*dnat to ([0-9A-Za-z.:\[\]-]+)(?: comment "([^"]*)")?`)

func lkParsePhone(text string) (map[string]string, map[string]string) {
	rules, comments := map[string]string{}, map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		m := lkPhoneRule.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rules[m[1]] = m[2]
		if m[3] != "" {
			comments[m[1]] = m[3]
		}
	}
	return rules, comments
}

// lkUnitEnv evaluates the Environment= lines of a unit file and its drop-ins
// in order (systemd semantics: later assignments win, an empty
// "Environment=" resets the list).
func lkUnitEnv(texts []string) map[string]string {
	env := map[string]string{}
	for _, text := range texts {
		section := ""
		for _, raw := range strings.Split(text, "\n") {
			line := strings.TrimSpace(raw)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			if strings.HasPrefix(line, "[") {
				section = line
				continue
			}
			if section != "[Service]" {
				continue
			}
			value, ok := strings.CutPrefix(line, "Environment=")
			if !ok {
				continue
			}
			if strings.TrimSpace(value) == "" {
				env = map[string]string{}
				continue
			}
			for _, tok := range lkSplitEnvWords(value) {
				if k, v, ok := strings.Cut(tok, "="); ok && k != "" {
					env[k] = v
				}
			}
		}
	}
	return env
}

// lkSplitEnvWords splits an Environment= value into assignments (double
// quotes group, backslash escapes inside quotes).
func lkSplitEnvWords(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && inQuote && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			have = true
		case c == '"':
			inQuote = !inQuote
			have = true
		case (c == ' ' || c == '\t') && !inQuote:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

var lkBuildRe = regexp.MustCompile(`build ([A-Za-z0-9._-]+)`)
var lkLogswitchRe = regexp.MustCompile(`logswitch (v[0-9][0-9.]*)`)

func lkUnitDescription(text string) string {
	for _, raw := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(raw), "Description="); ok {
			return v
		}
	}
	return ""
}

// lkSameReport: the parts that make a report worth sending again early.
func lkReportKey(r *LeiKwanHostReport) string {
	c := *r
	c.At = 0
	c.Traffic = nil
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
