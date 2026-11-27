package repo

import (
	"net"
	"strings"

	"go-backend/internal/store/model"
)

// Plan 321 (user 11-28): a node with several entry addresses (Leikwanhost
// 沪台IPLC: 电信 IPv6 DDNS, 联通 IPv6 DDNS, the vendor's IPv4 name) showed
// them all under the same name. Every entry address now carries a small tag
// derived from the node's DDNS records: the carrier (电信 / 联通 / 移动, from
// the record's source ct / cu / cm or the admin's per-record choice) and
// 「IPv6」 when the address only resolves to IPv6. IPv4 gets no tag. A user's
// 专属域名 carries the tag of the record it points at. Display only —
// nothing here changes an address, a link or a subscription.

// EntryLabel is the tag of one entry address.
type EntryLabel struct {
	Carrier string `json:"carrier,omitempty"` // 电信 / 联通 / 移动
	IPv6    bool   `json:"ipv6,omitempty"`
}

// Empty: nothing to show.
func (l EntryLabel) Empty() bool { return l.Carrier == "" && !l.IPv6 }

// Carrier codes of node_ddns_record.carrier ("" = from the source).
const (
	EntryCarrierAuto = ""
	EntryCarrierNone = "none"
)

// ValidEntryCarrier: "" (auto), ct, cu, cm, none.
func ValidEntryCarrier(c string) bool {
	switch c {
	case EntryCarrierAuto, "ct", "cu", "cm", EntryCarrierNone:
		return true
	}
	return false
}

// DDNSRecordCarrier is the carrier code a record shows ("" = none): the
// admin's choice, else the carrier its IPv6 source is read from.
func DDNSRecordCarrier(rec model.NodeDDNSRecord) string {
	switch c := strings.ToLower(strings.TrimSpace(rec.Carrier)); c {
	case "ct", "cu", "cm":
		return c
	case EntryCarrierNone:
		return ""
	}
	switch s := strings.ToLower(strings.TrimSpace(rec.Source)); s {
	case "ct", "cu", "cm":
		return s
	}
	return ""
}

// EntryCarrierLabel: ct → 电信, cu → 联通, cm → 移动.
func EntryCarrierLabel(code string) string {
	switch code {
	case "ct":
		return "电信"
	case "cu":
		return "联通"
	case "cm":
		return "移动"
	}
	return ""
}

// EntryLabels maps entry hosts (DDNS domains, their current IPs, users'
// names) to their tags.
type EntryLabels struct {
	byHost map[string]EntryLabel
	// byNode: the tag of a node's main entry (node-level lists such as the
	// 利群入口 picker), only when the node has exactly one DDNS domain.
	byNode map[int64]EntryLabel
}

func normLabelHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if ip := net.ParseIP(h); ip != nil {
		return ip.String()
	}
	return strings.TrimSuffix(h, ".")
}

// LoadEntryLabels reads every DDNS record and every handed-out name (2
// queries; a few hundred rows at most).
func (r *Repository) LoadEntryLabels() (*EntryLabels, error) {
	x := &EntryLabels{byHost: map[string]EntryLabel{}, byNode: map[int64]EntryLabel{}}
	if r == nil || r.db == nil {
		return x, nil
	}
	recs, hasA, err := r.ddnsRecordsByNode()
	if err != nil {
		return nil, err
	}
	type dom struct {
		a, aaaa bool
		carrier string
		mixed   bool
	}
	domains := map[string]*dom{}
	nodeDomains := map[int64]map[string]bool{}
	for nodeID, list := range recs {
		for _, rec := range list {
			d := strings.ToLower(strings.TrimSpace(rec.Domain))
			if d == "" {
				continue
			}
			v := domains[d]
			if v == nil {
				v = &dom{}
				domains[d] = v
			}
			switch strings.ToUpper(rec.RecordType) {
			case "A":
				v.a = true
			case "AAAA":
				v.aaaa = true
			}
			if c := DDNSRecordCarrier(rec); c != "" {
				if v.carrier != "" && v.carrier != c {
					v.mixed = true
				}
				v.carrier = c
			}
			if nodeDomains[nodeID] == nil {
				nodeDomains[nodeID] = map[string]bool{}
			}
			nodeDomains[nodeID][d] = true
			// The record's current IP stands for it (nodes whose address field
			// holds the IP).
			if ip := net.ParseIP(strings.Trim(strings.TrimSpace(rec.CurrentIP), "[]")); ip != nil {
				l := EntryLabel{Carrier: EntryCarrierLabel(DDNSRecordCarrier(rec)), IPv6: ip.To4() == nil}
				if !l.Empty() {
					x.byHost[ip.String()] = l
				}
			}
		}
	}
	for d, v := range domains {
		l := EntryLabel{IPv6: v.aaaa && !v.a && !hasA[d]}
		if !v.mixed {
			l.Carrier = EntryCarrierLabel(v.carrier)
		}
		if !l.Empty() {
			x.byHost[d] = l
		}
	}
	for nodeID, set := range nodeDomains {
		if len(set) != 1 {
			continue
		}
		for d := range set {
			if l, ok := x.byHost[d]; ok {
				x.byNode[nodeID] = l
			}
		}
	}
	// Users' names: the tag of the record they point at.
	var names []model.UserSubdomain
	if err := r.db.Select("node_id", "domain", "record_domain").
		Where("state <> ? OR blocked = ?", model.UserSubdomainRetired, true).Find(&names).Error; err != nil {
		return nil, err
	}
	for _, n := range names {
		target := strings.ToLower(strings.TrimSpace(n.RecordDomain))
		if target == "" {
			target = UserSubdomainMainOf(recs[n.NodeID], hasA).Domain
		}
		if l, ok := x.byHost[target]; ok && n.Domain != "" {
			x.byHost[normLabelHost(n.Domain)] = l
		}
	}
	return x, nil
}

// Of returns the tag of one host (a bare IPv6 literal is always 「IPv6」).
func (x *EntryLabels) Of(host string) (EntryLabel, bool) {
	h := normLabelHost(host)
	if h == "" {
		return EntryLabel{}, false
	}
	if x != nil {
		if l, ok := x.byHost[h]; ok {
			return l, true
		}
	}
	if ip := net.ParseIP(h); ip != nil && ip.To4() == nil {
		return EntryLabel{IPv6: true}, true
	}
	return EntryLabel{}, false
}

// Node returns the tag of a node's single DDNS domain (node-level lists).
func (x *EntryLabels) Node(nodeID int64) (EntryLabel, bool) {
	if x == nil {
		return EntryLabel{}, false
	}
	l, ok := x.byNode[nodeID]
	return l, ok
}

// ForList tags the hosts of a "host:port,host:port" (or bare host) list;
// nil when none has a tag. Keys are the hosts as written (no port), lower
// case, IPv6 without brackets.
func (x *EntryLabels) ForList(list string) map[string]EntryLabel {
	var out map[string]EntryLabel
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if l, ok := x.Of(part); ok && !l.Empty() {
			if out == nil {
				out = map[string]EntryLabel{}
			}
			out[normLabelHost(part)] = l
		}
	}
	return out
}
