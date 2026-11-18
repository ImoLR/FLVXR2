package service

import (
	"net"
)

// QUIC detection for filtered UDP sessions (plan 283).
//
// 「屏蔽 TLS」 on a tunnel also blocks QUIC on the UDP side of its forwards:
// QUIC is TLS 1.3 inside UDP (hysteria2, TUIC, Juicity, HTTP/3, Xray /
// sing-box QUIC), its Initial header is plaintext and the ClientHello inside
// can be decrypted by anyone (the Initial keys derive from the DCID).
//
// A client opens every QUIC connection with an Initial packet: a long header
// whose type bits are not header-protected, a version from a short list, two
// connection IDs of at most 20 bytes, a token and a length that fit the
// datagram — and RFC 9000 §14.1 makes the client pad every datagram carrying
// an Initial to at least 1200 bytes. Checking that takes a handful of byte
// compares on bytes the listener already handed us (no copy, no allocation,
// no decryption). Random-looking UDP — SS / SS2022, hy2 with Salamander,
// mieru, KCP — matches about once per 1e13 datagrams of ≥ 1200 bytes (plan
// doc 283 has the math, quic_detect_test.go the fuzz run); DNS, WireGuard,
// RTP, STUN and DTLS never do (first byte / version layout).

const (
	quicMinInitialDatagram = 1200 // RFC 9000 §14.1
	quicMaxCIDLen          = 20   // RFC 9000 §17.2 (QUIC v1 / v2)
	quicMinInitialLength   = 20   // packet number + 16-byte sample for header protection

	quicVersion1       = 0x00000001
	quicVersion2       = 0x6b3343cf // RFC 9369
	quicDraftFirst     = 0xff00001b // draft-27
	quicDraftLast      = 0xff000022 // draft-34
	quicGoogleQ046     = 0x51303436 // "Q046": one length byte, two nibbles
	quicGoogleQ050     = 0x51303530 // "Q050"
	quicGoogleT050     = 0x54303530 // "T050"
	quicGoogleT051     = 0x54303531 // "T051"
	quicGoogleClientCI = 8          // gQUIC clients use 8-byte connection IDs
)

// isQUICInitial reports whether b is a UDP datagram that starts with a client
// QUIC Initial packet.
func isQUICInitial(b []byte) bool {
	if len(b) < quicMinInitialDatagram || b[0]&0xC0 != 0xC0 { // long header + fixed bit
		return false
	}
	typ := (b[0] >> 4) & 0x03 // long packet type, not header-protected
	switch v := uint32(b[1])<<24 | uint32(b[2])<<16 | uint32(b[3])<<8 | uint32(b[4]); {
	case v == quicVersion1 || (v >= quicDraftFirst && v <= quicDraftLast):
		if typ != 0 {
			return false
		}
	case v == quicVersion2:
		if typ != 1 {
			return false
		}
	case v == quicGoogleQ046:
		// DCIL/SCIL nibbles (len-3): client DCID 8, no SCID.
		return typ == 0 && b[5] == (quicGoogleClientCI-3)<<4
	case v == quicGoogleQ050 || v == quicGoogleT050 || v == quicGoogleT051:
		return typ == 0 && b[5] == quicGoogleClientCI && b[6+quicGoogleClientCI] == 0
	default:
		return false
	}
	// IETF long header (v1 / v2 / drafts 27–34): DCID, SCID, Token, Length.
	p := 5
	if n := int(b[p]); n <= quicMaxCIDLen {
		p += 1 + n
	} else {
		return false
	}
	if n := int(b[p]); n <= quicMaxCIDLen {
		p += 1 + n
	} else {
		return false
	}
	tokenLen, k := quicVarint(b[p:])
	if k == 0 || tokenLen > uint64(len(b)-p-k) {
		return false
	}
	p += k + int(tokenLen)
	length, k := quicVarint(b[p:])
	return k > 0 && length >= quicMinInitialLength && length <= uint64(len(b)-p-k)
}

// quicVarint decodes a QUIC variable-length integer (RFC 9000 §16); k = 0
// when b is too short.
func quicVarint(b []byte) (v uint64, k int) {
	if len(b) == 0 {
		return 0, 0
	}
	k = 1 << (b[0] >> 6)
	if len(b) < k {
		return 0, 0
	}
	v = uint64(b[0] & 0x3f)
	for i := 1; i < k; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, k
}

// quicSession is the per-client session of a datagram listener
// (internal/net/udp): Block ends it and makes the listener drop that
// client's datagrams for the rest of the entry's idle lifetime (1–2 TTLs).
type quicSession interface {
	net.Conn
	Block()
}

// quicGuardConn checks every datagram a client sends into a forward whose
// tunnel blocks TLS. One length compare per datagram below 1200 bytes, one
// more byte compare for three in four larger ones; the full check runs only
// on long-header-looking datagrams.
type quicGuardConn struct {
	quicSession
}

func (c *quicGuardConn) Read(b []byte) (int, error) {
	n, err := c.quicSession.Read(b)
	if n >= quicMinInitialDatagram && b[0]&0xC0 == 0xC0 && isQUICInitial(b[:n]) {
		recordQUICBlock()
		c.quicSession.Block()
		return 0, errConnectionBlocked
	}
	return n, err
}

func recordQUICBlock() {
	blockCounts[blockKindQUIC].Add(1)
	blockLogs.Record("quic", "🚫 BLOCK QUIC (hy2/TUIC/HTTP3)")
}

// wrapUDPSession: a filtered forward's UDP session gets the QUIC check when
// its tunnel blocks TLS, and nothing at all otherwise (HTTP / SOCKS / other
// detection reads TCP first bytes only; UDP was always passed through).
func wrapUDPSession(s quicSession, flags protocolBlockFlags) net.Conn {
	if flags.tls == 1 {
		return &quicGuardConn{quicSession: s}
	}
	return s
}
